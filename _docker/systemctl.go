package main

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"time"
)

var serviceUnit = regexp.MustCompile(`\A(.+)\.service\z`)

var services = map[string]string{
	"icinga2":        "/lib/systemd/system/icinga2.service",
	"apache2":        "/lib/systemd/system/apache2.service",
	"mariadb":        "/lib/systemd/system/mariadb.service",
	"influxdb":       "/usr/lib/influxdb/scripts/influxdb.service",
	"grafana-server": "/usr/lib/systemd/system/grafana-server.service",
}

func main() {
	os.Exit(systemctl())
}

func systemctl() int {
	switch len(os.Args) {
	case 2:
		if reflect.DeepEqual(os.Args, []string{"/bin/systemctl", "list-units"}) {
			for service := range services {
				fmt.Printf("%s.service\n", service)
			}

			return 0
		}
	case 11:
		if reflect.DeepEqual(os.Args[:10], []string{
			"/bin/systemctl", "show",
			"-p", "ActiveState",
			"-p", "SubState",
			"-p", "ExecMainStartTimestamp",
			"-p", "FragmentPath",
		}) {
			if match := serviceUnit.FindStringSubmatch(os.Args[10]); match != nil {
				service := match[1]

				if fragmentPath, hasFP := services[service]; hasFP {
					var activeSince time.Time

					if time.Now().Unix()%120 < 60 {
						activeSince = time.Now()
					} else {
						activeSince = time.Date(1971, 1, 1, 0, 0, 0, 0, time.UTC)
					}

					fmt.Printf(
						"ActiveState=active\nSubState=running\nExecMainStartTimestamp=%s\nFragmentPath=%s\n",
						activeSince.Format("Mon 2006-01-02 15:04:05 MST"),
						fragmentPath,
					)
					return 0
				}
			}

			return 1
		}
	}

	return 1
}
