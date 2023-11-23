package main

import (
	"flag"

	"CaiJinKen/gostruct-ast/engine"
)

func main() {
	flag.Parse()
	engine := engine.New()
	engine.Run()
}
