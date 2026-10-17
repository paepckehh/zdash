{...}: {
  ##################
  #-=# SERVICES #=-#
  ##################
  services.caddy = {
    enable = true;
    virtualHosts."http://zdash".extraConfig = "reverse_proxy 127.0.0.1:9091";
  };

  #################
  #-=# SYSTEMD #=-#
  #################
  systemd = {
    sockets.zdash = {
      description = "zDash Socket (socket-activated ZFS dashboard)";
      wantedBy = ["sockets.target"];
      socketConfig.ListenStream = "127.0.0.1:9091";
    };
    services.zdash = {
      description = "zDash ZFS Dashboard";
      after = ["network.target"];
      environment = {
        BIND_ADDR = "127.0.0.1:9091";
        EXIT_ON_IDLE = "1";
        SOCKET_ACTIVATION = "1";
      };
      serviceConfig = {
        ExecStart = "/nix/persist/root/bin/zdash";
        KillMode = "mixed";
        Restart = "on-failure";
        RestartSec = "2s";
        StartLimitIntervalSec = 0;
        UMask = "0077";
        Nice = 10;
        CPUWeight = 10;
        IOWeight = 10;
        MemoryMax = "128M";
        TasksMax = 32;
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
        RestrictAddressFamilies = [
          "AF_INET"
          "AF_INET6"
          "AF_UNIX"
        ];
        SystemCallFilter = ["@system-service"];
        SocketBindDeny = "any";
        SocketBindAllow = "tcp:9091";
        IPAddressAllow = [
          "127.0.0.0/8"
          "::1"
        ];
        IPAddressDeny = ["any"];
      };
    };
  };
}
