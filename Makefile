VERSION ?= 0.1.0
PUBKEY  ?=
LDFLAGS  = -X github.com/billkaat/billkaat/internal/buildinfo.Version=$(VERSION)
ifneq ($(PUBKEY),)
LDFLAGS += -X github.com/billkaat/billkaat/internal/license.PublicKeyHex=$(PUBKEY)
endif

.PHONY: run demo test build build-pro keygen clean

run:            ## run the Community build from source
	go run .

demo:           ## run with a fake seeded scan (no AWS needed)
	go run . -demo -db demo.db

test:
	go test ./...

build:          ## Community binary
	CGO_ENABLED=1 go build -ldflags "$(LDFLAGS)" -o bin/billkaat .

build-pro:      ## Pro binary — set PUBKEY=<hex from licensegen keygen>
	CGO_ENABLED=1 go build -tags pro -ldflags "$(LDFLAGS)" -o bin/billkaat-pro .

keygen:         ## generate the Ed25519 signing key pair (run ONCE, store safely)
	go run ./cmd/licensegen keygen

clean:
	rm -rf bin *.db *.db-shm *.db-wal demo.db
