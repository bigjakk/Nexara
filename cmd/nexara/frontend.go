package main

import "embed"

//go:embed all:dist
var frontendDist embed.FS
