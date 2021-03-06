package main

import (
	"bytes"
	. "github.com/Al2Klimov/go-exec-utils"
	linux "github.com/Al2Klimov/go-linux-apis"
	"regexp"
	"time"
)

type serviceInfo struct {
	activeSince time.Time
	anyFile     string
}

type servicesInfo struct {
	services      map[string]serviceInfo
	servicesTotal uint64
	errs          map[string]error
}

type systemdInfo struct {
	serviceInfo
	errs map[string]error
}

type initExe struct {
	path string
	errs map[string]error
}

type systemctlShowResult struct {
	service      string
	cmd          string
	activeSince  time.Time
	fragmentPath string
	err          error
}

var serviceUnit = regexp.MustCompile(`\A(.+)\.service\z`)
var serviceProperty = regexp.MustCompile(`\A([^=]+)=(.*)\z`)

func showServices(ch chan<- servicesInfo) {
	cmd, unitFiles, errLUF := System("systemctl", []string{"list-units"}, map[string]string{"LC_ALL": "C"}, "/")
	if errLUF != nil {
		ch <- servicesInfo{errs: map[string]error{cmd: errLUF}}
		return
	}

	chSystemdInfo := make(chan systemdInfo, 1)
	chSystemctlShow := make(chan systemctlShowResult, 64)
	var servicesTotal uint64 = 0

	go getSystemdInfo(chSystemdInfo)

	for _, line := range bytes.Split(unitFiles, lineBreak)[1:] {
		line = bytes.Trim(line, " \t\r\n")

		if len(line) < 1 {
			break
		}

		if match1 := firstWord.FindSubmatch(line); match1 != nil {
			if match2 := serviceUnit.FindSubmatch(match1[1]); match2 != nil {
				go showService(string(match2[1]), chSystemctlShow)
				servicesTotal++
			}
		}
	}

	unitFiles = nil

	var result systemctlShowResult
	services := map[string]serviceInfo{}
	errSSS := map[string]error{}

	for pending := servicesTotal; pending > 0; pending-- {
		if result = <-chSystemctlShow; result.err == nil {
			if result.activeSince != (time.Time{}) {
				services[result.service] = serviceInfo{activeSince: result.activeSince, anyFile: result.fragmentPath}
			}
		} else {
			errSSS[result.cmd] = result.err
		}
	}

	close(chSystemctlShow)

	if syIn := <-chSystemdInfo; syIn.errs == nil {
		services["systemd"] = syIn.serviceInfo
	} else {
		for c, e := range syIn.errs {
			errSSS[c] = e
		}
	}

	if len(errSSS) > 0 {
		ch <- servicesInfo{errs: errSSS}
		return
	}

	ch <- servicesInfo{services: services, servicesTotal: servicesTotal, errs: nil}
}

func getSystemdInfo(ch chan<- systemdInfo) {
	if uptime, errGUT := linux.GetUptime(); errGUT == nil {
		ch <- systemdInfo{
			serviceInfo: serviceInfo{activeSince: time.Now().Add(-uptime.UpTime), anyFile: "/sbin/init"},
			errs:        nil,
		}
	} else {
		ch <- systemdInfo{errs: map[string]error{"cat /proc/uptime": errGUT}}
	}
}

func showService(service string, ch chan<- systemctlShowResult) {
	cmd, rawProperties, errSSS := System(
		"systemctl", []string{
			"show",
			"-p", "ActiveState",
			"-p", "SubState",
			"-p", "ExecMainStartTimestamp",
			"-p", "FragmentPath",
			service + ".service",
		},
		map[string]string{"LC_ALL": "C"},
		"/",
	)
	if errSSS != nil {
		ch <- systemctlShowResult{cmd: cmd, err: errSSS}
		return
	}

	properties := make(map[string]string, 3)

	for _, line := range bytes.Split(rawProperties, lineBreak) {
		if match := serviceProperty.FindSubmatch(line); match != nil {
			properties[string(match[1])] = string(match[2])
		}
	}

	var activeSince time.Time
	if properties["ActiveState"] == "active" && properties["SubState"] == "running" {
		var errTP error
		activeSince, errTP = time.Parse("Mon 2006-01-02 15:04:05 MST", properties["ExecMainStartTimestamp"])

		if errTP != nil {
			activeSince = time.Time{}
		}
	}

	ch <- systemctlShowResult{
		service:      service,
		cmd:          cmd,
		activeSince:  activeSince,
		fragmentPath: properties["FragmentPath"],
		err:          nil,
	}
}
