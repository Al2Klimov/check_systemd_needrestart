package main

import "sync/atomic"

func showPackages(ch chan packagesInfo) {
	packages, errs := dpkgShowPackages()
	if errs != nil {
		ch <- packagesInfo{errs: errs}
		return
	}

	packageEffectiveAliases := map[string]map[string]struct{}{}

	for packag, pkgInfo := range packages.packages {
		for alias := range pkgInfo.aliases {
			if packages, isAlias := packageEffectiveAliases[alias]; isAlias {
				packages[packag] = struct{}{}
			} else {
				packageEffectiveAliases[alias] = map[string]struct{}{packag: {}}
			}
		}

		pkgInfo.aliases = nil
	}

	greyAliases := packageEffectiveAliases

	for {
		whiteAliases := map[string]map[string]struct{}{}

		for alias, packages := range greyAliases {
			blackAliases := packageEffectiveAliases[alias]
			var freshPackages map[string]struct{} = nil

			for packag := range packages {
				if _, isAlias := packageEffectiveAliases[packag]; isAlias {
					for packag2 := range packageEffectiveAliases[packag] {
						if _, aliased := blackAliases[packag2]; !aliased {
							if freshPackages == nil {
								freshPackages = map[string]struct{}{}
								whiteAliases[alias] = freshPackages
							}

							freshPackages[packag2] = struct{}{}
						}
					}
				}
			}
		}

		if len(whiteAliases) < 1 {
			break
		}

		greyAliases = whiteAliases

		for alias, packages := range greyAliases {
			blackAliases := packageEffectiveAliases[alias]

			for packag := range packages {
				blackAliases[packag] = struct{}{}
			}
		}
	}

	greyAliases = nil

	pendingUnaliasDeps := uint64(len(packages.packages))
	chUnaliasDepsDone := make(chan struct{}, 1)

	for _, pkgInfo := range packages.packages {
		unaliasDeps(pkgInfo.deps, packageEffectiveAliases, &pendingUnaliasDeps, chUnaliasDepsDone)
	}

	packageEffectiveAliases = nil

	<-chUnaliasDepsDone
	chUnaliasDepsDone = nil

	pendingCompressDeps := uint64(len(packages.packages))
	chCompressDepsDone := make(chan struct{}, 1)

	for _, pkgInfo := range packages.packages {
		compressDeps(pkgInfo.deps, packages.packages, &pendingCompressDeps, chCompressDepsDone)
	}

	<-chCompressDepsDone
	chCompressDepsDone = nil

	greyDeps := make(map[string]map[string]struct{}, len(packages.packages))

	for packag, pkgInfo := range packages.packages {
		greyDeps[packag] = pkgInfo.deps
	}

	for {
		whiteDeps := map[string]map[string]struct{}{}

		for packag, deps := range greyDeps {
			blackDeps := packages.packages[packag].deps
			var freshDeps map[string]struct{} = nil

			for dep := range deps {
				for depDep := range packages.packages[dep].deps {
					if _, hasDep := blackDeps[depDep]; !hasDep {
						if freshDeps == nil {
							freshDeps = map[string]struct{}{}
							whiteDeps[packag] = freshDeps
						}

						freshDeps[depDep] = struct{}{}
					}
				}
			}
		}

		if len(whiteDeps) < 1 {
			break
		}

		greyDeps = whiteDeps

		for packag, deps := range greyDeps {
			blackDeps := packages.packages[packag].deps

			for dep := range deps {
				blackDeps[dep] = struct{}{}
			}
		}
	}

	greyDeps = nil

	for packag, pkgInfo := range packages.packages {
		pkgInfo.deps[packag] = struct{}{}
	}

	ch <- packages
}

func unaliasDeps(deps map[string]struct{}, aliases map[string]map[string]struct{}, pending *uint64, chDone chan struct{}) {
	newDeps := map[string]struct{}{}

	for dep := range deps {
		if _, isAlias := aliases[dep]; isAlias {
			for packag := range aliases[dep] {
				if _, aliased := deps[packag]; !aliased {
					newDeps[packag] = struct{}{}
				}
			}
		}
	}

	for dep := range newDeps {
		deps[dep] = struct{}{}
	}

	if atomic.AddUint64(pending, ^uint64(0)) == 0 {
		chDone <- struct{}{}
	}
}

func compressDeps(deps map[string]struct{}, packages map[string]packageInfo, pending *uint64, chDone chan struct{}) {
	oldDeps := map[string]struct{}{}

	for dep := range deps {
		if _, isActualPackage := packages[dep]; !isActualPackage {
			oldDeps[dep] = struct{}{}
		}
	}

	for dep := range oldDeps {
		delete(deps, dep)
	}

	if atomic.AddUint64(pending, ^uint64(0)) == 0 {
		chDone <- struct{}{}
	}
}
