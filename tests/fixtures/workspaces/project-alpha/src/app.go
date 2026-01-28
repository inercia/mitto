package src

import "fmt"

// Run starts the application
func Run() {
	fmt.Println("Running application...")
	result := Add(2, 3)
	fmt.Printf("2 + 3 = %d\n", result)
}
