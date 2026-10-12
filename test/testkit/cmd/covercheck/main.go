// covercheck fails when a coverage profile leaves statements in the chosen
// files uncovered: CI's Windows job runs it for *_windows.go, whose code the
// 100% gate on Linux and macOS never compiles.
//
//	go run ./test/testkit/cmd/covercheck -suffix _windows.go cover.out
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

func main() {
	suffix := flag.String("suffix", "", "check only files ending in this")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: covercheck -suffix _windows.go cover.out")
		os.Exit(2)
	}
	f, err := os.Open(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer f.Close()
	// A block appears once per test binary: covered if any run counted it.
	stmts, hits := map[string]int{}, map[string]int{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var block string
		var n, count int
		if _, err := fmt.Sscanf(sc.Text(), "%s %d %d", &block, &n, &count); err != nil || n == 0 {
			continue // the "mode:" line, or an empty block
		}
		file, _, _ := strings.Cut(block, ":")
		if !strings.HasSuffix(file, *suffix) {
			continue
		}
		stmts[block] = n
		hits[block] += count
	}
	var missed []string
	total := 0
	for b, n := range stmts {
		total += n
		if hits[b] == 0 {
			missed = append(missed, b)
		}
	}
	sort.Strings(missed)
	for _, b := range missed {
		fmt.Println("not covered:", b)
	}
	if len(missed) > 0 {
		os.Exit(1)
	}
	fmt.Printf("%d statements in *%s: all covered\n", total, *suffix)
}
