package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/sunxu/relay-station-control/internal/assetcredential"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "asset credential key provisioning failed")
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("relay-control-asset-credential-key", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("path", "", "destination for the external asset credential key")
	modeText := flags.String("mode", "0600", "file mode: 0400 or 0600")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *path == "" || flags.NArg() != 0 {
		return errors.New("--path is required and no positional arguments are accepted")
	}
	parsedMode, err := strconv.ParseUint(*modeText, 8, 32)
	if err != nil {
		return errors.New("--mode must be octal 0400 or 0600")
	}
	mode := os.FileMode(parsedMode)
	if mode != 0o400 && mode != 0o600 {
		return errors.New("--mode must be octal 0400 or 0600")
	}
	_, statErr := os.Lstat(*path)
	previouslyExists := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	if err := assetcredential.ProvisionKeyFromOS(*path, mode); err != nil {
		return err
	}
	status := "created"
	if previouslyExists {
		status = "preserved"
	}
	_, err = fmt.Fprintln(stdout, "asset credential key "+status)
	return err
}
