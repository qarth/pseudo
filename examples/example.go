package main

import (
	"fmt"
	"github.com/qarth/pseudo2"
)

func main() {
	fmt.Println("Starting...")
	pseudo.PseudoCtx.LowestLabel = false
	pseudo.PseudoCtx.FifoBucket = true

	results, err := pseudo.Run("dimacsMaxf.txt")

	if err != nil {
		fmt.Printf("%v", err)
	}

	fmt.Println("Finished.")
	fmt.Printf("Results = %v", results)
	//results2 := pseudo.Result("")
	//fmt.Printf("Results2 = %v", results2)

}
