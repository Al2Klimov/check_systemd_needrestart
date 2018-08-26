package main

import (
	"bytes"
	"regexp"
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

	packages := map[string]map[string][][]byte{}
	var pending uint64 = 0
	chDpkgList := make(chan dpkgShowPackageResult, 64)
	packag := ""
	attr := ""
	readingList := false

	for _, line := range bytes.Split(rawPackages, lineBreak) {
		if len(line) > 0 {
			if readingList {
				if line[0] == ' ' {
					if match := firstWord.FindSubmatch(line[1:]); match != nil {
						attrs := packages[packag]
						attrs[attr] = append(attrs[attr], match[1])
					}
				} else {
					readingList = false
				}
			}

			if !readingList {
				if match := dpkgProperty.FindSubmatch(line); match != nil {
					attr = string(match[1])

					if attr == "Package" {
						if packag != "" {
							attrs := packages[packag]

							if _, installed := dpkgParseStatus(attrs["Status"])["installed"]; installed {
								go dpkgShowPackage(
									packag,
									attrs["Conffiles"],
									attrs["Depends"],
									attrs["Pre-Depends"],
									attrs["Provides"],
									attrs["Replaces"],
									chDpkgList,
								)

								pending++
							}
						}

						packag = string(match[3])
						packages[packag] = make(map[string][][]byte, 4)
					} else if packag != "" {
						switch match[2][0] {
						case '=':
							packages[packag][attr] = [][]byte{match[3]}
						case '>':
							packages[packag][attr] = [][]byte{}
							readingList = true
						}
					}
				}
			}
		}
	}

	if packag != "" {
		attrs := packages[packag]

		if _, installed := dpkgParseStatus(attrs["Status"])["installed"]; installed {
			go dpkgShowPackage(
				packag,
				attrs["Conffiles"],
				attrs["Depends"],
				attrs["Pre-Depends"],
				attrs["Provides"],
				attrs["Replaces"],
				chDpkgList,
			)

			pending++
		}
	}

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

func dpkgShowPackage(
	packag string,
	conffiles [][]byte,
	depends [][]byte,
	preDepends [][]byte,
	provides [][]byte,
	replaces [][]byte,
	ch chan dpkgShowPackageResult,
) {
	chEffectiveDeps := make(chan map[string]struct{}, 1)
	chEffectiveAliases := make(chan map[string]struct{}, 1)

	go dpkgParsePackagesLists(chEffectiveDeps, depends, preDepends)
	go dpkgParsePackagesLists(chEffectiveAliases, provides, replaces)

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

	for _, file := range conffiles {
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

func dpkgParsePackagesLists(ch chan map[string]struct{}, lists ...[][]byte) {
	result := map[string]struct{}{}

	for _, list := range lists {
		for _, packages := range list {
			for _, packag := range bytes.Split(packages, commaSpace) {
				if match := firstWord.FindSubmatch(packag); match != nil {
					result[string(match[1])] = struct{}{}
				}
			}
		}
	}

	ch <- result
}
