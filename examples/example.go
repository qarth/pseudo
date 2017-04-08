package main

import (
	"fmt"
	"github.com/qarth/pseudo2"
)

func main() {
	fmt.Println("Starting...")
	pseudo.PseudoCtx.LowestLabel = false
	pseudo.PseudoCtx.FifoBucket = true

	pseudo.Run("dimacsMaxf.txt")
	fmt.Println("Finished.")

}
