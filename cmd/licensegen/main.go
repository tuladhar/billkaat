// licensegen issues billkaat Pro licenses.
//
// The tool itself is safe to publish — Ed25519's security lives entirely in
// the PRIVATE key, which you generate once, store offline (password manager
// + printed backup), and never commit anywhere.
//
//	go run ./cmd/licensegen keygen
//	go run ./cmd/licensegen sign  -key <PRIVATE_HEX> -email buyer@x.com -name "Buyer Co"
//	go run ./cmd/licensegen verify -pub <PUBLIC_HEX> -license <KEY>
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/billkaat/billkaat/internal/license"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "keygen":
		keygen()
	case "sign":
		sign(os.Args[2:])
	case "verify":
		verify(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  licensegen keygen
  licensegen sign   -key <PRIVATE_HEX> -email <email> -name <name>
  licensegen verify -pub <PUBLIC_HEX> -license <KEY>`)
	os.Exit(2)
}

func keygen() {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fatal(err)
	}
	fmt.Printf(`PRIVATE key (KEEP SECRET — this is the whole business):
%s

PUBLIC key (compile into release binaries):
%s

Build releases with:
  go build -ldflags "-X github.com/billkaat/billkaat/internal/license.PublicKeyHex=%s" .
`, hex.EncodeToString(priv), hex.EncodeToString(pub), hex.EncodeToString(pub))
}

func sign(args []string) {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	key := fs.String("key", "", "private key hex")
	email := fs.String("email", "", "buyer email")
	name := fs.String("name", "", "buyer name or company")
	_ = fs.Parse(args)
	if *key == "" || *email == "" {
		fs.Usage()
		os.Exit(2)
	}
	id := make([]byte, 6)
	if _, err := rand.Read(id); err != nil {
		fatal(err)
	}
	lic, err := license.Sign(*key, license.Payload{
		V:      1,
		ID:     hex.EncodeToString(id),
		Email:  *email,
		Name:   *name,
		Plan:   "pro",
		Issued: time.Now().UTC().Format("2006-01-02"),
	})
	if err != nil {
		fatal(err)
	}
	fmt.Println(lic)
}

func verify(args []string) {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	pub := fs.String("pub", "", "public key hex")
	lic := fs.String("license", "", "license key")
	_ = fs.Parse(args)
	p, err := license.VerifyWithKey(*pub, *lic)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("VALID — plan=%s id=%s email=%s name=%q issued=%s\n",
		p.Plan, p.ID, p.Email, p.Name, p.Issued)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
