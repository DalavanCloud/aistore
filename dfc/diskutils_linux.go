// Package dfc is a scalable object-storage based caching system with Amazon and Google Cloud backends.
/*
 * Copyright (c) 2018, NVIDIA CORPORATION. All rights reserved.
 *
 */
package dfc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/NVIDIA/dfcpub/3rdparty/glog"
)

const (
	iostatnumsys     = 6
	iostatnumdsk     = 14
	iostatMinVersion = 11
)

// newIostatRunner initalizes iostatrunner struct with default values
func newIostatRunner() *iostatrunner {
	return &iostatrunner{
		stopCh:      make(chan struct{}, 1),
		Disk:        make(map[string]simplekvs),
		metricnames: make([]string, 0),
	}
}

type LsBlk struct {
	BlockDevices []BlockDevice `json:"blockdevices"`
}

type BlockDevice struct {
	Name         string        `json:"name"`
	BlockDevices []BlockDevice `json:"children"`
}

// iostat -cdxtm 10
func (r *iostatrunner) run() error {
	r.updateFSDisks()
	refreshPeriod := int(ctx.config.Periodic.StatsTime / time.Second)
	cmd := exec.Command("iostat", "-cdxtm", strconv.Itoa(refreshPeriod))
	stdout, err := cmd.StdoutPipe()
	reader := bufio.NewReader(stdout)
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}

	// Assigning started process
	r.process = cmd.Process

	glog.Infof("Starting %s", r.name)

	for {
		b, err := reader.ReadBytes('\n')
		if err != nil {
			return err
		}

		line := string(b)
		fields := strings.Fields(line)
		if len(fields) == iostatnumsys {
			r.Lock()
			r.CPUidle = fields[iostatnumsys-1]
			r.Unlock()
		} else if len(fields) >= iostatnumdsk {
			if strings.HasPrefix(fields[0], "Device") {
				if len(r.metricnames) == 0 {
					r.metricnames = append(r.metricnames, fields[1:]...)
				}
			} else {
				r.Lock()
				device := fields[0]
				var (
					iometrics simplekvs
					ok        bool
				)
				if iometrics, ok = r.Disk[device]; !ok {
					iometrics = make(simplekvs, len(fields)-1) // first time
				}
				for i := 1; i < len(fields); i++ {
					name := r.metricnames[i-1]
					iometrics[name] = fields[i]
				}
				r.Disk[device] = iometrics
				r.Unlock()
			}
		}
		select {
		case <-r.stopCh:
			return nil
		default:
		}
	}
}

func (r *iostatrunner) stop(err error) {
	glog.Infof("Stopping %s, err: %v", r.name, err)
	var v struct{}
	r.stopCh <- v
	close(r.stopCh)

	// Kill process if started
	if r.process != nil {
		if err := r.process.Kill(); err != nil {
			glog.Errorf("Failed to kill iostat, err: %v", err)
		}
	}
}

func (r *iostatrunner) isZeroUtil(dev string) bool {
	iometrics := r.Disk[dev]
	if utilstr, ok := iometrics["%util"]; ok {
		if util, err := strconv.ParseFloat(utilstr, 32); err == nil {
			if util == 0 {
				return true
			}
		}
	}
	return false
}

func (r *iostatrunner) updateFSDisks() {
	availablePaths, _ := ctx.mountpaths.Mountpaths()
	r.Lock()
	r.fsdisks = make(map[string]StringSet, len(availablePaths))
	for _, mpathInfo := range availablePaths {
		disks := fs2disks(mpathInfo.FileSystem)
		if len(disks) == 0 {
			glog.Errorf("filesystem (%+v) - no disks?", mpathInfo)
			continue
		}
		r.fsdisks[mpathInfo.FileSystem] = disks
	}
	r.Unlock()
}

func (r *iostatrunner) diskUtilFromFQN(fqn string) (util float32, ok bool) {
	fs := fqn2fs(fqn)
	if fs == "" {
		return
	}
	return r.maxUtilFS(fs)
}

func (r *iostatrunner) maxUtilFS(fs string) (util float32, ok bool) {
	r.RLock()
	disks, isOk := r.fsdisks[fs]
	if !isOk {
		r.RUnlock()
		return
	}
	util = float32(maxUtilDisks(r.Disk, disks))
	r.RUnlock()
	if util < 0 {
		return
	}
	return util, true
}

// NOTE: Since this invokes a shell command, it is slow.
// Do not use this in code paths which are executed per object.
// This method is used only while starting the iostat runner to
// retrieve the disks associated with a file system.
func fs2disks(fs string) (disks StringSet) {
	getDiskCommand := exec.Command("lsblk", "-no", "name", "-J")
	outputBytes, err := getDiskCommand.Output()
	if err != nil || len(outputBytes) == 0 {
		glog.Errorf("Unable to retrieve disks from FS [%s].", fs)
		return
	}

	disks = lsblkOutput2disks(outputBytes, fs)
	return
}

func childMatches(devList []BlockDevice, device string) bool {
	for _, dev := range devList {
		if dev.Name == device {
			return true
		}

		if len(dev.BlockDevices) != 0 && childMatches(dev.BlockDevices, device) {
			return true
		}
	}

	return false
}

func findDevDisks(devList []BlockDevice, device string, disks StringSet) {
	for _, bd := range devList {
		if bd.Name == device {
			disks[bd.Name] = struct{}{}
			continue
		}
		if len(bd.BlockDevices) != 0 {
			if childMatches(bd.BlockDevices, device) {
				disks[bd.Name] = struct{}{}
			}
		}
	}
}

func lsblkOutput2disks(lsblkOutputBytes []byte, fs string) (disks StringSet) {
	disks = make(StringSet)
	device := strings.TrimPrefix(fs, "/dev/")
	var lsBlkOutput LsBlk
	err := json.Unmarshal(lsblkOutputBytes, &lsBlkOutput)
	if err != nil {
		glog.Errorf("Unable to unmarshal lsblk output [%s]. Error: [%v]", string(lsblkOutputBytes), err)
		return
	}

	findDevDisks(lsBlkOutput.BlockDevices, device, disks)
	if glog.V(3) {
		glog.Info("Device: %s, disk list: %v\n", device, disks)
	}

	return disks
}

// checkIostatVersion determines whether iostat command is present and
// is not too old (at least version `iostatMinVersion` is required).
func checkIostatVersion() error {
	cmd := exec.Command("iostat", "-V")

	vbytes, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("[iostat] Error: %v", err)
	}

	vwords := strings.Split(string(vbytes), "\n")
	if vwords = strings.Split(vwords[0], " "); len(vwords) < 3 {
		return fmt.Errorf("[iostat] Error: unknown iostat version format %v", vwords)
	}

	vss := strings.Split(vwords[2], ".")
	if len(vss) < 3 {
		return fmt.Errorf("[iostat] Error: unexpected version format: %v", vss)
	}

	version, err := strconv.ParseInt(vss[0], 10, 64)
	if err != nil {
		return fmt.Errorf("[iostat] Error: failed to parse version %v, error %v", vss, err)
	}

	if version < iostatMinVersion {
		return fmt.Errorf("[iostat] Error: version %v is too old. At least %v version is required", version, iostatMinVersion)
	}

	return nil
}
