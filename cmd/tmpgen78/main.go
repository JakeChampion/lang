package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/jakechampion/lang/internal/fernsmith"
)

func main() {
	n, _ := strconv.ParseUint(os.Args[1], 10, 64)
	fmt.Print(fernsmith.GenMain(n))
}
