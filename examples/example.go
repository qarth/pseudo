package main

import (
	"fmt"
	"github.com/qarth/pseudo"
)

func main() {
	fmt.Println("Starting...")
	pseudo.PseudoCtx.LowestLabel = true
	pseudo.PseudoCtx.FifoBucket = true

	results, err := pseudo.Run("/home/rob/go/src/github.com/qarth/pseudo/examples/dimacsMaxf.txt")

	if err != nil {
		fmt.Printf("%v", err)
	}

	fmt.Println("Finished.")
	fmt.Printf("Results = %v", results)
	//results2 := pseudo.Result("")
	//fmt.Printf("Results2 = %v", results2)

}
