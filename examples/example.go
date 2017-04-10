package main

import (
	"fmt"
	"github.com/qarth/pseudo2"
)

func main() {
	fmt.Println("Starting...")
	pseudo.PseudoCtx.LowestLabel = false
	pseudo.PseudoCtx.FifoBucket = true

	pseudo.Run("dimacsMcf.txt")
	fmt.Println("Finished.")
	results := pseudo.Result("")
	fmt.Printf("Ret = %v", results)

}
