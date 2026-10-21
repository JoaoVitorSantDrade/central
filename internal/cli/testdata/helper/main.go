// Comando helper dos testes e2e de central rodar, para não depender de shell:
// exit N sai com N; sleep S dorme S segundos; lines N imprime as linhas 1..N.
package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "uso: helper exit N | sleep S | lines N")
		os.Exit(100)
	}
	n, err := strconv.ParseFloat(os.Args[2], 64)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(100)
	}
	switch os.Args[1] {
	case "exit":
		fmt.Fprintf(os.Stderr, "saindo com %v\n", n)
		os.Exit(int(n))
	case "sleep":
		time.Sleep(time.Duration(n * float64(time.Second)))
	case "lines":
		for i := 1; i <= int(n); i++ {
			fmt.Printf("linha %d\n", i)
		}
	default:
		fmt.Fprintln(os.Stderr, "subcomando desconhecido:", os.Args[1])
		os.Exit(100)
	}
}
