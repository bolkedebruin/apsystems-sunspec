BIN  := ecu-sunspec
PKG  := ./cmd/ecu-sunspec
DIST := dist

# Build flags: pure Go, stripped, optimized.
LDFLAGS := -s -w
GOFLAGS := -trimpath -ldflags '$(LDFLAGS)'

.PHONY: all test ecu airbender mac clean

all: ecu airbender

# ECU: AM335x, ARMv7, glibc 2.15. CGO disabled — modernc.org/sqlite is pure Go.
ecu: $(DIST)/$(BIN)-linux-armv7

$(DIST)/$(BIN)-linux-armv7:
	@mkdir -p $(DIST)
	GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 \
		go build $(GOFLAGS) -o $@ $(PKG)
	@ls -lh $@

# airbender: Synology r1000_1522+, x86_64.
airbender: $(DIST)/$(BIN)-linux-amd64

$(DIST)/$(BIN)-linux-amd64:
	@mkdir -p $(DIST)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
		go build $(GOFLAGS) -o $@ $(PKG)
	@ls -lh $@

# Local development build.
mac: $(DIST)/$(BIN)-darwin
$(DIST)/$(BIN)-darwin:
	@mkdir -p $(DIST)
	go build $(GOFLAGS) -o $@ $(PKG)

test:
	go test ./...

clean:
	rm -rf $(DIST)
