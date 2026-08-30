package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/migrations"
)

func main() {
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		panic(err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		data, err := migrations.FS.ReadFile(n)
		if err != nil {
			panic(err)
		}
		content := strings.TrimSpace(string(data))
		sum := sha256.Sum256([]byte(content))
		fmt.Printf("%s|%s\n", n, hex.EncodeToString(sum[:]))
	}
}
