//+build ignore

package main

import (
	"flag"
	"fmt"
	"io/ioutil"
)

func main() {
	flag.Parse()
	for _, path := range flag.Args() {
		bs, err := ioutil.ReadFile(path)
		if err != nil {
			panic(err)
		}
		fmt.Printf("%#v\n", bs)
	}
}
