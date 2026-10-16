{
  config,
  infra,
  ...
}: {
  ##################
  #-=# SERVICES #=-#
  ##################
  services.caddy.virtualHosts."${config.networking.hostName}.${infra.domain.user}" = {
    extraConfig = ''import intraproxy ${toString infra.zdash.localbind.port.http}'';
  };

  #################
  #-=# SYSTEMD #=-#
  #################

  # Socket activation: systemd holds the bound listener and zdash is not
  # started at boot at all — the first dashboard request spawns it. Combined
  # with ZDASH_EXIT_ON_IDLE below, the process exists only while somebody is
  # actually looking at the dashboard; idle RAM and CPU are exactly zero.
  systemd.sockets.zdash = {
    description = "zDash Socket (socket-activated ZFS dashboard)";
    wantedBy = ["sockets.target"];
    socketConfig = {
      ListenStream = "${infra.localhost.ip}:${toString infra.zdash.localbind.port.http}";
    };
  };

  systemd.services.zdash = {
    description = "zDash ZFS Dashboard";
    after = ["network.target"];
    # No wantedBy: the socket unit above is the only activator. Booting the
    # service eagerly would defeat socket activation entirely.
    environment = {
      # Opt in to adopting the systemd-passed socket fd (Linux-only feature,
      # disabled by default in the binary).
      ZDASH_SOCKET_ACTIVATION = "1";
      # Exit with status 0 once the last session closes instead of parking
      # in deep sleep; the socket unit restarts on demand. The systemd
      # sleep profile below becomes a safety net for the wake window.
      ZDASH_EXIT_ON_IDLE = "1";
      # BIND_ADDR is only a fallback when the socket unit is not used; the
      # real listener is the socket-activation fd passed by systemd.
      BIND_ADDR = "${infra.localhost.ip}:${toString infra.zdash.localbind.port.http}";
    };
    serviceConfig = {
      ExecStart = "/nix/persist/root/bin/zdash";
      KillMode = "mixed"; # main process plus any straggling zpool/smartctl child
      Restart = "on-failure";
      RestartSec = "2s";
      # Exit-on-idle terminates with status 0 over and over by design;
      # never rate-limit the on-demand restarts.
      StartLimitIntervalSec = 0;
      UMask = "0077";

      # --- resource usage ---
      # Low scheduling priority: a dashboard must never compete with the
      # storage stack it is monitoring.
      Nice = 10;
      CPUWeight = 10;
      IOWeight = 10;
      MemoryMax = "128M";
      # 32 tasks: the main process plus parallel smartctl/zpool children.
      TasksMax = 32;

      # --- sandboxing / hardening ---
      NoNewPrivileges = true;
      ProtectSystem = "strict";
      ProtectHome = true;
      PrivateTmp = true;
      ProtectKernelTunables = true;
      ProtectKernelModules = true;
      ProtectKernelLogs = true;
      ProtectControlGroups = true;
      ProtectClock = true;
      ProtectHostname = true;
      ProtectProc = "invisible";
      LockPersonality = true;
      RestrictRealtime = true;
      RestrictSUIDSGID = true;
      MemoryDenyWriteExecute = true;
      RestrictNamespaces = true;
      SystemCallArchitectures = "native";
      RestrictAddressFamilies = ["AF_INET" "AF_INET6" "AF_UNIX"];
      # @system-service covers ioctl, needed by the ZFS ioctls via /dev/zfs
      # and by smartctl. Deliberately NOT restricted: capability bounding
      # and @privileged syscalls, because zpool/smartctl run as root and
      # speak to /dev/zfs and raw disk devices.
      SystemCallFilter = ["@system-service"];
      # Only loopback traffic (the Caddy reverse proxy on this host) may
      # talk to the dashboard; no egress for the service or its children.
      IPAddressAllow = ["127.0.0.0/8" "::1"];
      IPAddressDeny = ["any"];
    };
  };
}
