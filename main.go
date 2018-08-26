package main

import (
	"fmt"
	"html"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
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

type orderedFile struct {
	path string
	diff time.Duration
}

type orderedPackage struct {
	name  string
	files []orderedFile
}

type orderedService struct {
	name     string
	packages []orderedPackage
	pending  uint64
}

var firstWord = regexp.MustCompile(`\A(\S+)`)
var ignoredFile = regexp.MustCompile(`\A/(?:dev|etc|run|tmp|var|usr/share/(?:doc|man|locale))/`)
var lineBreak = []byte("\n")

var shortOutput = struct {
	table [2][]byte
	tr    [3][]byte
}{
	table: [2][]byte{
		[]byte("<p><b>Some services have not been restarted since some of their parts have been upgraded:</b></p>" +
			"<table><thead><tr><th>Service</th><th>Packages</th><th>Upgrade - service start</th></tr></thead><tbody>"),
		[]byte("</tbody></table>\n\n"),
	},
	tr: [3][]byte{[]byte("<tr><td>"), []byte("</td><td>"), []byte("</td></tr>")},
}

var longOutput = struct {
	h1    [2][]byte
	h2    [2][]byte
	table [2][]byte
	tr    [3][]byte
}{
	h1: [2][]byte{[]byte("<p><b>Service: "), []byte("</b></p>")},
	h2: [2][]byte{[]byte("<p>Package: "), []byte("</p>")},
	table: [2][]byte{
		[]byte("<table><thead><tr><th>File</th><th>MTime - service start</th></tr></thead><tbody>"),
		[]byte("</tbody></table>"),
	},
	tr: [3][]byte{[]byte("<tr><td>"), []byte("</td><td>"), []byte("</td></tr>")},
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

	var status int
	var output string

	if len(serviceDiffs) > 0 {
		status = 2
		output = assembleCriticalOutput(orderCriticalOutput(serviceDiffs))
	} else {
		status = 0
		output = "<p>No service has not been restarted since some of its parts have been upgraded.</p>"
	}

	if _, errFP := fmt.Print(output); errFP != nil {
		status = 3
	}

	os.Exit(status)

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

func orderCriticalOutput(serviceDiffs map[string]map[string]map[string]time.Duration) []orderedService {
	services := make([]orderedService, len(serviceDiffs))
	serviceIdx := 0
	pending := uint64(len(services))
	chDone := make(chan struct{}, 1)

	for service, packageDiffs := range serviceDiffs {
		packages := make([]orderedPackage, len(packageDiffs))
		packageIdx := 0
		serviceAddr := &services[serviceIdx]
		*serviceAddr = orderedService{name: service, packages: packages, pending: uint64(len(packages))}

		for packag, fileDiffs := range packageDiffs {
			files := make([]orderedFile, len(fileDiffs))
			fileIdx := 0
			packages[packageIdx] = orderedPackage{name: packag, files: files}

			for file, diff := range fileDiffs {
				files[fileIdx] = orderedFile{path: file, diff: diff}
				fileIdx++
			}

			go orderTree(files, packages, serviceAddr, &pending, services, chDone)

			packageIdx++
		}

		serviceIdx++
	}

	<-chDone
	close(chDone)

	return services
}

func orderTree(files []orderedFile, packages []orderedPackage, service *orderedService, pending *uint64, services []orderedService, chDone chan struct{}) {
	sort.Slice(files, func(i, j int) bool {
		a := files[i]
		b := files[j]

		if a.diff == b.diff {
			return a.path < b.path
		}

		return a.diff > b.diff
	})

	if atomic.AddUint64(&service.pending, ^uint64(0)) == 0 {
		sort.Slice(packages, func(i, j int) bool {
			a := packages[i]
			b := packages[j]
			aDiff := a.files[0].diff
			bDiff := b.files[0].diff

			if aDiff == bDiff {
				return a.name < b.name
			}

			return aDiff > bDiff
		})

		if atomic.AddUint64(pending, ^uint64(0)) == 0 {
			sort.Slice(services, func(i, j int) bool {
				a := services[i]
				b := services[j]
				aDiff := a.packages[0].files[0].diff
				bDiff := b.packages[0].files[0].diff

				if aDiff == bDiff {
					return a.name < b.name
				}

				return aDiff > bDiff
			})

			chDone <- struct{}{}
		}
	}
}

func assembleCriticalOutput(services []orderedService) string {
	builder := strings.Builder{}

	builder.Write(shortOutput.table[0])

	for _, service := range services {
		builder.Write(shortOutput.tr[0])
		builder.Write([]byte(html.EscapeString(service.name)))
		builder.Write(shortOutput.tr[1])
		builder.Write([]byte(strconv.FormatInt(int64(len(service.packages)), 10)))
		builder.Write(shortOutput.tr[1])
		builder.Write([]byte(html.EscapeString(service.packages[0].files[0].diff.String())))
		builder.Write(shortOutput.tr[2])
	}

	builder.Write(shortOutput.table[1])

	for _, service := range services {
		builder.Write(longOutput.h1[0])
		builder.Write([]byte(html.EscapeString(service.name)))
		builder.Write(longOutput.h1[1])

		for _, packag := range service.packages {
			builder.Write(longOutput.h2[0])
			builder.Write([]byte(html.EscapeString(packag.name)))
			builder.Write(longOutput.h2[1])
			builder.Write(longOutput.table[0])

			for _, file := range packag.files {
				builder.Write(longOutput.tr[0])
				builder.Write([]byte(html.EscapeString(file.path)))
				builder.Write(longOutput.tr[1])
				builder.Write([]byte(html.EscapeString(file.diff.String())))
				builder.Write(longOutput.tr[2])
			}

			builder.Write(longOutput.table[1])
		}
	}

	return builder.String()
}
