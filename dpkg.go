package main

import (
	"bytes"
	"regexp"
	"strings"
)

type dpkgShowPackageResult struct {
	packag  string
	files   map[string]struct{}
	deps    map[string]struct{}
	aliases map[string]struct{}
	cmd     string
	err     error
}

var dpkgProperty = regexp.MustCompile(`\A([^=>]+)([=>])(.*)\z`)
var anyWord = regexp.MustCompile(`\S+`)
var commaSpace = []byte(", ")

func dpkgShowPackages() (packagesInfo, map[string]error) {
	cmd, rawPackages, errDQ := system(
		"dpkg-query", "-W",
		"-f", `Package=${Package}
Architecture=${Architecture}
Status=${Status}
Depends=${Depends}
Pre-Depends=${Pre-Depends}
Provides=${Provides}
Replaces=${Replaces}
Conffiles>
${Conffiles}
`,
		"*",
	)
	if errDQ != nil {
		return packagesInfo{}, map[string]error{cmd: errDQ}
	}

	var pending uint64 = 0
	chDpkgList := make(chan dpkgShowPackageResult, 64)
	attrs := map[string][][]byte{}
	attr := ""
	var values [][]byte = nil

	for _, line := range bytes.Split(rawPackages, lineBreak) {
		if len(line) > 0 {
			if values != nil {
				if line[0] == ' ' {
					if match := firstWord.FindSubmatch(line[1:]); match != nil {
						values = append(values, match[1])
					}
				} else {
					attrs[attr] = values
					values = nil
				}
			}

			if values == nil {
				if match := dpkgProperty.FindSubmatch(line); match != nil {
					attr = string(match[1])

					if attr == "Package" {
						if _, hasPackage := attrs["Package"]; hasPackage {
							if _, installed := dpkgParseStatus(attrs["Status"])["installed"]; installed {
								go dpkgShowPackage(attrs, chDpkgList)
								pending++
							}
						}

						attrs = make(map[string][][]byte, 8)
					}

					switch match[2][0] {
					case '=':
						attrs[attr] = [][]byte{match[3]}
					case '>':
						values = [][]byte{}
					}
				}
			}
		}
	}

	if _, hasPackage := attrs["Package"]; hasPackage {
		if _, installed := dpkgParseStatus(attrs["Status"])["installed"]; installed {
			go dpkgShowPackage(attrs, chDpkgList)
			pending++
		}
	}

	attrs = nil
	values = nil

	packageMetaData := make(map[string]packageInfo, pending)
	nonConfFiles := map[string]string{}
	errs := map[string]error{}

	for ; pending > 0; pending-- {
		if files := <-chDpkgList; files.err == nil {
			packageMetaData[files.packag] = packageInfo{
				deps:         files.deps,
				aliases:      files.aliases,
				nonConfFiles: files.files,
			}

			for file := range files.files {
				nonConfFiles[file] = files.packag
			}
		} else {
			errs[files.cmd] = files.err
		}
	}

	close(chDpkgList)

	if len(errs) > 0 {
		return packagesInfo{}, errs
	}

	return packagesInfo{packages: packageMetaData, nonConfFiles: nonConfFiles}, nil
}

func dpkgParseStatus(status [][]byte) (result map[string]struct{}) {
	result = map[string]struct{}{}

	for _, stats := range status {
		if matches := anyWord.FindAll(stats, -1); matches != nil {
			for _, match := range matches {
				result[string(match)] = struct{}{}
			}
		}
	}

	return
}

func dpkgShowPackage(attrs map[string][][]byte, ch chan dpkgShowPackageResult) {
	arch := dpkgExtractStringAttr(attrs, "Architecture")

	chEffectiveDeps := make(chan map[string]struct{}, 1)
	chEffectiveAliases := make(chan map[string]struct{}, 1)

	go dpkgParsePackagesLists(chEffectiveDeps, [2][][]byte{attrs["Depends"], attrs["Pre-Depends"]}, []string{arch, "all"})
	go dpkgParsePackagesLists(chEffectiveAliases, [2][][]byte{attrs["Provides"], attrs["Replaces"]}, []string{arch})

	packag := dpkgExtractStringAttr(attrs, "Package") + ":" + arch

	cmd, rawFiles, errDL := system("dpkg", "-L", packag)
	if errDL != nil {
		<-chEffectiveDeps
		<-chEffectiveAliases
		ch <- dpkgShowPackageResult{cmd: cmd, err: errDL}
		return
	}

	lines := bytes.Split(rawFiles, lineBreak)
	files := make(map[string]struct{}, len(lines)-1)

	for _, line := range lines {
		if len(line) > 0 {
			files[string(line)] = struct{}{}
		}
	}

	delete(files, "/.")

	for _, file := range attrs["Conffiles"] {
		delete(files, string(file))
	}

	ch <- dpkgShowPackageResult{
		packag:  packag,
		files:   files,
		deps:    <-chEffectiveDeps,
		aliases: <-chEffectiveAliases,
		cmd:     cmd,
		err:     nil,
	}
}

func dpkgExtractStringAttr(attrs map[string][][]byte, attr string) string {
	if values := attrs[attr]; len(values) > 0 {
		return string(values[0])
	}

	return ""
}

func dpkgParsePackagesLists(ch chan map[string]struct{}, lists [2][][]byte, archs []string) {
	result := map[string]struct{}{}

	for _, list := range lists {
		for _, packages := range list {
			for _, packag := range bytes.Split(packages, commaSpace) {
				if match := firstWord.FindSubmatch(packag); match != nil {
					if packag := string(match[1]); strings.Contains(packag, ":") {
						result[packag] = struct{}{}
					} else {
						for _, arch := range archs {
							result[packag+":"+arch] = struct{}{}
						}
					}
				}
			}
		}
	}

	ch <- result
}
