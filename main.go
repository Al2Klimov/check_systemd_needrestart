package main

import (
	"fmt"
	"html"
	"os"
	"regexp"
	"strings"
	"time"
)

type packageInfo struct {
	deps         map[string]struct{}
	nonConfFiles map[string]struct{}
}

type packagesInfo struct {
	packages     map[string]packageInfo
	nonConfFiles map[string]string
	errs         map[string]error
}

type nonConfFilesScan struct {
	nonConfFiles map[string]time.Time
	errs         map[string]error
}

type mTimesDiff struct {
	service string
	diffs   map[string]map[string]time.Duration
}

var firstWord = regexp.MustCompile(`\A(\S+)`)
var ignoredFile = regexp.MustCompile(`\A/(?:dev|etc|run|tmp|var|usr/share/(?:doc|man|locale))/`)
var lineBreak = []byte("\n")
var h1 = [2][]byte{[]byte("<p><b>Service: "), []byte("</b></p>")}
var h2 = [2][]byte{[]byte("<p>Package: "), []byte("</p>")}
var tr = [3][]byte{[]byte("<tr><td>"), []byte("</td><td>"), []byte("</td></tr>")}

var table = [2][]byte{
	[]byte("<table><thead><tr><th>File</th><th>MTime - service start</th></tr></thead><tbody>"),
	[]byte("</tbody></table>"),
}

func main() {
	if errsCSNR := checkSystemdNeedrestart(); errsCSNR != nil {
		for context, err := range errsCSNR {
			fmt.Printf("%s: %s\n", context, err.Error())
		}

		os.Exit(3)
	}
}

func checkSystemdNeedrestart() map[string]error {
	chPackagesInfo := make(chan packagesInfo, 1)
	chServicesInfo := make(chan servicesInfo, 1)

	go dpkgShowPackages(chPackagesInfo)
	go showServices(chServicesInfo)

	packages := <-chPackagesInfo
	services := <-chServicesInfo

	close(chPackagesInfo)
	close(chServicesInfo)

	chPackagesInfo = nil
	chServicesInfo = nil

	var errs map[string]error = nil

	if services.errs != nil {
		errs = services.errs
	}

	if packages.errs != nil {
		if errs == nil {
			errs = packages.errs
		} else {
			for context, err := range packages.errs {
				errs[context] = err
			}
		}
	}

	if errs != nil {
		return errs
	}

	chNonConfFilesScan := make(chan nonConfFilesScan, 64)
	packagesHandled := map[string]struct{}{}
	serviceDeps := map[string]map[string]struct{}{}

	for name, service := range services.services {
		if packag, hasPackage := packages.nonConfFiles[service.fragmentPath]; hasPackage {
			if _, handled := packagesHandled[packag]; !handled {
				go scanNonConfFiles(packages.packages[packag].nonConfFiles, chNonConfFilesScan)
				packagesHandled[packag] = struct{}{}
			}

			deps := map[string]struct{}{packag: {}}
			serviceDeps[name] = deps

			for {
				depsLen := len(deps)

				for packag2 := range deps {
					for dep := range packages.packages[packag2].deps {
						if metaData, hasMetaData := packages.packages[dep]; hasMetaData {
							if _, hasDep := deps[dep]; !hasDep {
								deps[dep] = struct{}{}

								if _, handled := packagesHandled[dep]; !handled {
									go scanNonConfFiles(metaData.nonConfFiles, chNonConfFilesScan)
									packagesHandled[dep] = struct{}{}
								}
							}
						}
					}
				}

				if len(deps) == depsLen {
					break
				}
			}
		}
	}

	mTimes := map[string]time.Time{}
	errs = map[string]error{}

	for pending := len(packagesHandled); pending > 0; pending-- {
		if scan := <-chNonConfFilesScan; scan.errs == nil {
			for file, mTime := range scan.nonConfFiles {
				mTimes[file] = mTime
			}
		} else {
			for context, err := range scan.errs {
				errs[context] = err
			}
		}
	}

	close(chNonConfFilesScan)
	chNonConfFilesScan = nil
	packagesHandled = nil

	if len(errs) > 0 {
		return errs
	}

	chMTimesDiff := make(chan mTimesDiff, 64)

	for service, deps := range serviceDeps {
		go diffMTimes(service, services.services[service].activeSince, deps, packages.packages, mTimes, chMTimesDiff)
	}

	packages = packagesInfo{}
	services = servicesInfo{}
	mTimes = nil

	serviceDiffs := map[string]map[string]map[string]time.Duration{}

	for pending := len(serviceDeps); pending > 0; pending-- {
		if diffs := <-chMTimesDiff; len(diffs.diffs) > 0 {
			serviceDiffs[diffs.service] = diffs.diffs
		}
	}

	close(chMTimesDiff)
	chMTimesDiff = nil
	serviceDeps = nil

	if len(serviceDiffs) < 1 {
		os.Exit(0)
	}

	builder := strings.Builder{}

	for service, packageDiffs := range serviceDiffs {
		builder.Write(h1[0])
		builder.Write([]byte(html.EscapeString(service)))
		builder.Write(h1[1])

		for packag, fileDiffs := range packageDiffs {
			builder.Write(h2[0])
			builder.Write([]byte(html.EscapeString(packag)))
			builder.Write(h2[1])
			builder.Write(table[0])

			for file, diff := range fileDiffs {
				builder.Write(tr[0])
				builder.Write([]byte(html.EscapeString(file)))
				builder.Write(tr[1])
				builder.Write([]byte(html.EscapeString(diff.String())))
				builder.Write(tr[2])
			}

			builder.Write(table[1])
		}
	}

	if _, errFP := fmt.Print(builder.String()); errFP != nil {
		os.Exit(3)
	}

	os.Exit(2)

	return nil
}

func scanNonConfFiles(nonConfFiles map[string]struct{}, ch chan nonConfFilesScan) {
	mTimes := map[string]time.Time{}
	errs := map[string]error{}

	for file := range nonConfFiles {
		if ignoredFile.FindSubmatch([]byte(file)) == nil {
			if info, errStat := os.Stat(file); errStat == nil {
				if !info.IsDir() {
					mTimes[file] = info.ModTime()
				}
			} else if !os.IsNotExist(errStat) {
				errs[formatCmd("stat", file)] = errStat
			}
		}
	}

	if len(errs) > 0 {
		ch <- nonConfFilesScan{errs: errs}
	} else {
		ch <- nonConfFilesScan{nonConfFiles: mTimes, errs: nil}
	}
}

func diffMTimes(service string, activeSince time.Time, deps map[string]struct{}, packages map[string]packageInfo, mTimes map[string]time.Time, ch chan mTimesDiff) {
	diffs := map[string]map[string]time.Duration{}

	for dep := range deps {
		for file := range packages[dep].nonConfFiles {
			if mTime, hasMTime := mTimes[file]; hasMTime {
				if diff := mTime.Sub(activeSince); diff >= time.Duration(0) {
					if depDiffs, hasDep := diffs[dep]; hasDep {
						depDiffs[file] = diff
					} else {
						diffs[dep] = map[string]time.Duration{file: diff}
					}
				}
			}
		}
	}

	ch <- mTimesDiff{service: service, diffs: diffs}
}
