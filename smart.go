package zdash

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// nixSmartctl is the NixOS-specific smartctl path, mirrored after the zpool
// special-case in api.go: on NixOS smartctl is not on the default $PATH.
const nixSmartctl = "/run/current-system/sw/bin/smartctl"

// smartTimeout bounds each /api/smart request. Running smartctl --xall over a
// set of disks can take a few seconds (a sleeping HDD may need to spin up), so
// this is more generous than the 5s used for the zpool subprocess.
const smartTimeout = 30 * time.Second

// perDeviceTimeout bounds a single smartctl --xall invocation.
const perDeviceTimeout = 12 * time.Second

// SmartReport is the aggregated JSON returned by /api/smart. It runs
// `smartctl --scan --json` once to discover devices, then `smartctl --xall
// --json <dev>` for each in parallel, normalising the most dashboard-relevant
// fields into SmartDeviceReport. When smartctl is not installed the endpoint
// still responds 200 with SmartctlAvailable=false so the frontend can hide the
// SMART tabs gracefully (capability-driven, like the ZFS/ARC tabs).
type SmartReport struct {
	SmartctlAvailable bool                `json:"smartctl_available"`
	SmartctlVersion   string              `json:"smartctl_version,omitempty"`
	ScanError         string              `json:"scan_error,omitempty"`
	Timestamp         int64               `json:"timestamp_ms"`
	Devices           []SmartDeviceReport `json:"devices"`
}

// SmartDeviceReport pairs a scan entry with its normalised --xall details, or
// with an error string when the per-device probe failed.
type SmartDeviceReport struct {
	Name     string        `json:"name"`
	Type     string        `json:"type"`
	Protocol string        `json:"protocol"`
	OK       bool          `json:"ok"`
	Error    string        `json:"error,omitempty"`
	Details  *SmartDetails `json:"details,omitempty"`
}

// SmartDetails is the normalised, dashboard-facing subset of
// `smartctl --xall --json <dev>`. The raw smartctl schema varies a lot by
// transport (ATA vs NVMe vs SCSI); SmartDetails collapses the differences so
// the frontend renders one shape.
type SmartDetails struct {
	ModelName       string `json:"model_name"`
	ModelFamily     string `json:"model_family,omitempty"`
	SerialNumber    string `json:"serial_number"`
	FirmwareVersion string `json:"firmware_version"`
	FormFactor      string `json:"form_factor,omitempty"`
	RotationRate    int    `json:"rotation_rate,omitempty"`
	CapacityBytes   uint64 `json:"capacity_bytes"`
	Protocol        string `json:"protocol"`
	DeviceType      string `json:"device_type"`

	SmartPassed bool `json:"smart_passed"`

	// Temperature, in Celsius. Critical/Warning thresholds are 0 when the
	// transport does not expose them (e.g. plain SCSI).
	TemperatureCurrent  int `json:"temperature_current,omitempty"`
	TemperatureWarning  int `json:"temperature_warning,omitempty"`
	TemperatureCritical int `json:"temperature_critical,omitempty"`

	PowerOnHours    uint64 `json:"power_on_hours,omitempty"`
	PowerCycleCount uint64 `json:"power_cycle_count,omitempty"`

	// NVMe-only: populated from nvme_smart_health_information_log.
	NVMe *NVMeHealth `json:"nvme,omitempty"`
	// ATA-only: populated from ata_smart_attributes + self-test log.
	ATA *ATAHealth `json:"ata,omitempty"`
}

// NVMeHealth mirrors the fields of nvme_smart_health_information_log that
// matter for a health dashboard.
type NVMeHealth struct {
	CriticalWarning         int    `json:"critical_warning"`
	Temperature             int    `json:"temperature"`
	AvailableSpare          int    `json:"available_spare"`
	AvailableSpareThreshold int    `json:"available_spare_threshold"`
	PercentageUsed          int    `json:"percentage_used"`
	DataUnitsRead           uint64 `json:"data_units_read"`
	DataUnitsWritten        uint64 `json:"data_units_written"`
	HostReads               uint64 `json:"host_reads"`
	HostWrites              uint64 `json:"host_writes"`
	ControllerBusyTime      uint64 `json:"controller_busy_time"`
	PowerCycles             uint64 `json:"power_cycles"`
	PowerOnHours            uint64 `json:"power_on_hours"`
	UnsafeShutdowns         uint64 `json:"unsafe_shutdowns"`
	MediaErrors             uint64 `json:"media_errors"`
	NumErrLogEntries        uint64 `json:"num_err_log_entries"`
	WarningTempTime         uint64 `json:"warning_temp_time"`
	CriticalCompTime        uint64 `json:"critical_comp_time"`
	TemperatureSensors      []int  `json:"temperature_sensors,omitempty"`
}

// ATAHealth holds the ATA SMART attribute table plus a summary of the most
// recent self-test.
type ATAHealth struct {
	Attributes []ATAAttribute `json:"attributes"`
	SelfTest   ATASelfTest    `json:"self_test"`
}

// ATAAttribute is a single row from ata_smart_attributes.table, normalised.
type ATAAttribute struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Value      int    `json:"value"`
	Worst      int    `json:"worst"`
	Thresh     int    `json:"thresh"`
	WhenFailed string `json:"when_failed,omitempty"`
	RawValue   uint64 `json:"raw"`
	RawString  string `json:"raw_string"`
}

// ATASelfTest summarises the last entry of ata_smart_self_test_log.extended.
type ATASelfTest struct {
	Passed       bool   `json:"passed"`
	StatusString string `json:"status"`
	Hours        int    `json:"hours,omitempty"`
}

// rawSmartScan maps `smartctl --scan --json` output. Only the fields the
// dashboard reads are decoded; unknown keys are ignored.
type rawSmartScan struct {
	Smartctl struct {
		Version    []int `json:"version"`
		ExitStatus int   `json:"exit_status"`
	} `json:"smartctl"`
	Devices []SmartScanDevice `json:"devices"`
}

// SmartScanDevice is one entry from the scan devices array.
type SmartScanDevice struct {
	Name     string `json:"name"`
	InfoName string `json:"info_name"`
	Type     string `json:"type"`
	Protocol string `json:"protocol"`
}

// rawSmartAll maps `smartctl --xall --json <dev>` output. It is the parsing
// seam: the public SmartDetails is built from it via parseSmartAll. Keeping a
// raw struct here makes the parser testable with canned fixtures.
type rawSmartAll struct {
	ModelName    string `json:"model_name"`
	ModelFamily  string `json:"model_family"`
	SerialNumber string `json:"serial_number"`
	Firmware     string `json:"firmware_version"`
	FormFactor   struct {
		Name string `json:"name"`
	} `json:"form_factor"`
	RotationRate int `json:"rotation_rate"`
	UserCapacity struct {
		Bytes uint64 `json:"bytes"`
	} `json:"user_capacity"`
	Device struct {
		Type     string `json:"type"`
		Protocol string `json:"protocol"`
	} `json:"device"`
	SmartStatus struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	Temperature struct {
		Current          int `json:"current"`
		OpLimitMax       int `json:"op_limit_max"`
		CriticalLimitMax int `json:"critical_limit_max"`
	} `json:"temperature"`
	NVMeThresholds struct {
		Warning  int `json:"warning"`
		Critical int `json:"critical"`
	} `json:"nvme_composite_temperature_threshold"`
	PowerOnTime struct {
		Hours uint64 `json:"hours"`
	} `json:"power_on_time"`
	PowerCycleCount uint64 `json:"power_cycle_count"`
	NVMeHealth      *struct {
		CriticalWarning         int    `json:"critical_warning"`
		Temperature             int    `json:"temperature"`
		AvailableSpare          int    `json:"available_spare"`
		AvailableSpareThreshold int    `json:"available_spare_threshold"`
		PercentageUsed          int    `json:"percentage_used"`
		DataUnitsRead           uint64 `json:"data_units_read"`
		DataUnitsWritten        uint64 `json:"data_units_written"`
		HostReads               uint64 `json:"host_reads"`
		HostWrites              uint64 `json:"host_writes"`
		ControllerBusyTime      uint64 `json:"controller_busy_time"`
		PowerCycles             uint64 `json:"power_cycles"`
		PowerOnHours            uint64 `json:"power_on_hours"`
		UnsafeShutdowns         uint64 `json:"unsafe_shutdowns"`
		MediaErrors             uint64 `json:"media_errors"`
		NumErrLogEntries        uint64 `json:"num_err_log_entries"`
		WarningTempTime         uint64 `json:"warning_temp_time"`
		CriticalCompTime        uint64 `json:"critical_comp_time"`
		TemperatureSensors      []int  `json:"temperature_sensors"`
	} `json:"nvme_smart_health_information_log"`
	ATAAttributes *struct {
		Table []struct {
			ID         int    `json:"id"`
			Name       string `json:"name"`
			Value      int    `json:"value"`
			Worst      int    `json:"worst"`
			Thresh     int    `json:"thresh"`
			WhenFailed string `json:"when_failed"`
			Raw        struct {
				Value  uint64 `json:"value"`
				String string `json:"string"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
	ATASelfTestLog *struct {
		Extended struct {
			Table []struct {
				Status struct {
					Passed bool   `json:"passed"`
					String string `json:"string"`
				} `json:"status"`
				LifetimeHours int `json:"lifetime_hours"`
			} `json:"table"`
		} `json:"extended"`
	} `json:"ata_smart_self_test_log"`
}

// resolveSmartctlBin picks the smartctl binary to invoke. It prefers the NixOS
// layout (mirroring resolveZpoolBin) and falls back to a $PATH lookup.
func resolveSmartctlBin() string {
	if _, err := os.Stat(nixSmartctl); err == nil {
		return nixSmartctl
	}
	if path, err := exec.LookPath("smartctl"); err == nil {
		return path
	}
	return "smartctl"
}

// smartctlResolver is the seam used by HandleSmartAPI so tests can point the
// handler at a stubbed smartctl without depending on a real install.
var smartctlResolver = resolveSmartctlBin

// smartctlVersion formats the smartctl.version array as "7.5".
func smartctlVersion(v []int) string {
	if len(v) == 0 {
		return ""
	}
	parts := make([]string, 0, len(v))
	for _, n := range v {
		parts = append(parts, fmt.Sprintf("%d", n))
	}
	return strings.Join(parts, ".")
}

// fetchSmartScan runs `smartctl --scan --json` and decodes the device list.
func fetchSmartScan(ctx context.Context, bin string) (*rawSmartScan, error) {
	cmd := exec.CommandContext(ctx, bin, "--scan", "--json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("smartctl scan exec: %w", err)
	}
	var data rawSmartScan
	if err := json.Unmarshal(out, &data); err != nil {
		return nil, fmt.Errorf("parse smartctl scan json: %w", err)
	}
	return &data, nil
}

// fetchSmartDevice runs `smartctl --xall --json <dev>` and normalises the
// result into SmartDetails. A failing exit status (e.g. a SAT device that
// reports SMART status as failed) is NOT treated as an error here: smartctl
// returns non-zero for a failing drive, but the JSON is still valid and
// contains the failing health status the dashboard needs to surface.
func fetchSmartDevice(ctx context.Context, bin, dev string) (*SmartDetails, error) {
	cmd := exec.CommandContext(ctx, bin, "--xall", "--json", dev)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("smartctl exec %s: %w", dev, err)
	}
	var raw rawSmartAll
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse smartctl json %s: %w", dev, err)
	}
	return parseSmartAll(&raw), nil
}

// parseSmartAll maps the raw smartctl schema into SmartDetails. It is pure so
// it can be unit-tested with canned fixtures.
func parseSmartAll(raw *rawSmartAll) *SmartDetails {
	d := &SmartDetails{
		ModelName:           raw.ModelName,
		ModelFamily:         raw.ModelFamily,
		SerialNumber:        raw.SerialNumber,
		FirmwareVersion:     raw.Firmware,
		FormFactor:          raw.FormFactor.Name,
		RotationRate:        raw.RotationRate,
		CapacityBytes:       raw.UserCapacity.Bytes,
		Protocol:            raw.Device.Protocol,
		DeviceType:          raw.Device.Type,
		SmartPassed:         raw.SmartStatus.Passed,
		TemperatureCurrent:  raw.Temperature.Current,
		TemperatureWarning:  max(raw.NVMeThresholds.Warning, raw.Temperature.OpLimitMax),
		TemperatureCritical: max(raw.NVMeThresholds.Critical, raw.Temperature.CriticalLimitMax),
		PowerOnHours:        raw.PowerOnTime.Hours,
		PowerCycleCount:     raw.PowerCycleCount,
	}
	if raw.NVMeHealth != nil {
		h := raw.NVMeHealth
		d.NVMe = &NVMeHealth{
			CriticalWarning:         h.CriticalWarning,
			Temperature:             h.Temperature,
			AvailableSpare:          h.AvailableSpare,
			AvailableSpareThreshold: h.AvailableSpareThreshold,
			PercentageUsed:          h.PercentageUsed,
			DataUnitsRead:           h.DataUnitsRead,
			DataUnitsWritten:        h.DataUnitsWritten,
			HostReads:               h.HostReads,
			HostWrites:              h.HostWrites,
			ControllerBusyTime:      h.ControllerBusyTime,
			PowerCycles:             h.PowerCycles,
			PowerOnHours:            h.PowerOnHours,
			UnsafeShutdowns:         h.UnsafeShutdowns,
			MediaErrors:             h.MediaErrors,
			NumErrLogEntries:        h.NumErrLogEntries,
			WarningTempTime:         h.WarningTempTime,
			CriticalCompTime:        h.CriticalCompTime,
			TemperatureSensors:      h.TemperatureSensors,
		}
		if d.PowerOnHours == 0 {
			d.PowerOnHours = h.PowerOnHours
		}
		if d.PowerCycleCount == 0 {
			d.PowerCycleCount = h.PowerCycles
		}
		if d.TemperatureCurrent == 0 {
			d.TemperatureCurrent = h.Temperature
		}
	}
	if raw.ATAAttributes != nil {
		ata := &ATAHealth{}
		for _, a := range raw.ATAAttributes.Table {
			ata.Attributes = append(ata.Attributes, ATAAttribute{
				ID:         a.ID,
				Name:       a.Name,
				Value:      a.Value,
				Worst:      a.Worst,
				Thresh:     a.Thresh,
				WhenFailed: a.WhenFailed,
				RawValue:   a.Raw.Value,
				RawString:  a.Raw.String,
			})
		}
		if raw.ATASelfTestLog != nil && len(raw.ATASelfTestLog.Extended.Table) > 0 {
			last := raw.ATASelfTestLog.Extended.Table[len(raw.ATASelfTestLog.Extended.Table)-1]
			ata.SelfTest = ATASelfTest{
				Passed:       last.Status.Passed,
				StatusString: last.Status.String,
				Hours:        last.LifetimeHours,
			}
		}
		d.ATA = ata
	}
	return d
}

// HandleSmartAPI exposes the aggregated SMART snapshot at /api/smart. GET only.
// When smartctl is missing it returns 200 with smartctl_available=false so the
// frontend can suppress the SMART tabs without an error banner.
func HandleSmartAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), smartTimeout)
	defer cancel()

	bin := smartctlResolver()
	report := &SmartReport{Timestamp: time.Now().UnixMilli()}

	scan, err := fetchSmartScan(ctx, bin)
	if err != nil {
		// smartctl missing or scan failure: report capability as false so
		// the dashboard hides the SMART tabs instead of erroring.
		log.Printf("⚠️  smartctl scan failed: %v", err)
		report.SmartctlAvailable = false
		report.ScanError = err.Error()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(report)
		return
	}

	report.SmartctlAvailable = true
	report.SmartctlVersion = smartctlVersion(scan.Smartctl.Version)
	report.Devices = make([]SmartDeviceReport, 0, len(scan.Devices))

	// Probe every discovered device in parallel, each with its own timeout so
	// one sleeping HDD cannot blow the whole request.
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, dev := range scan.Devices {
		wg.Add(1)
		go func(d SmartScanDevice) {
			defer wg.Done()
			dctx, dcancel := context.WithTimeout(ctx, perDeviceTimeout)
			defer dcancel()
			details, derr := fetchSmartDevice(dctx, bin, d.Name)
			rep := SmartDeviceReport{
				Name:     d.Name,
				Type:     d.Type,
				Protocol: d.Protocol,
			}
			if derr != nil {
				rep.OK = false
				rep.Error = derr.Error()
			} else {
				rep.OK = true
				rep.Details = details
			}
			mu.Lock()
			report.Devices = append(report.Devices, rep)
			mu.Unlock()
		}(dev)
	}
	wg.Wait()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(report)
	_, _ = w.Write(buf.Bytes())
}
