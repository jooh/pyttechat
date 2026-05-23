package main

import (
	"fmt"

	"example.com/llm-chat-web/internal/buildinfo"
)

func main() {
	fmt.Println(buildinfo.Summary())
}
