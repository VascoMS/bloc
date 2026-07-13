package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = runAttribution(os.Args[2:])
	case "report":
		err = reportAttribution(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  bte-attribution run --batch-sizes 8,32,128 --warmups 5 --repetitions 30 --out-dir results/bte-attribution/host
  bte-attribution report --campaign-dir results/bte-attribution/campaign --out results/bte-attribution/campaign/RUN_REPORT.md`)
}
