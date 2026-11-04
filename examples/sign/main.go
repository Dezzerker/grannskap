// sign prints the three Grannskap headers for one call, so a handshake can be run with curl.
//
//	go run ./examples/sign -key <privat nyckel base64> -house https://hus.example.se -method POST -path /api/grannskap/v1/forfragan -body @forfragan.json
//
// Without -key it makes a new key pair and prints both halves first.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Dezzerker/grannskap"
)

func main() {
	key := flag.String("key", "", "privat nyckel (base64); tom = skapa ett nytt par")
	house := flag.String("house", "https://hus.example.se", "ert hus adress")
	method := flag.String("method", "GET", "HTTP-metod")
	path := flag.String("path", grannskap.PathWiki, "sökväg utan värdnamn")
	body := flag.String("body", "", "kropp, eller @fil")
	flag.Parse()
	if *key == "" {
		pub, priv, err := grannskap.NewKeyPair()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("public_key:", pub)
		fmt.Println("private_key:", priv)
		*key = priv
	}
	raw := []byte(*body)
	if strings.HasPrefix(*body, "@") {
		var err error
		if raw, err = os.ReadFile((*body)[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	ts := time.Now().Unix()
	sig, err := grannskap.Sign(*key, *method, *path, ts, raw)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%s: %s\n%s: %d\n%s: %s\n", grannskap.HeaderHouse, *house, grannskap.HeaderTimestamp, ts, grannskap.HeaderSignature, sig)
	fmt.Printf("\ncurl -s -X %s -H %q -H %q -H %q", *method, grannskap.HeaderHouse+": "+*house, fmt.Sprintf("%s: %d", grannskap.HeaderTimestamp, ts), grannskap.HeaderSignature+": "+sig)
	if len(raw) > 0 {
		fmt.Printf(" -H 'Content-Type: application/json' --data-binary %s", *body)
	}
	fmt.Printf(" <hus>%s\n", *path)
}
