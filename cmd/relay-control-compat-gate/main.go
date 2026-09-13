package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sunxu/relay-station-control/internal/compatgate"
)

func main() {
	args := os.Args[1:]
	commandArgs := []string(nil)
	for i, arg := range args {
		if arg == "--" {
			commandArgs = args[i+1:]
			args = args[:i]
			break
		}
	}
	flags := flag.NewFlagSet("relay-control-compat-gate", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	manifestPath := flags.String("manifest", compatgate.DefaultManifestPath, "signed manifest path")
	publicKeyPath := flags.String("public-key", compatgate.DefaultPublicKeyPath, "trusted Ed25519 public key path")
	artifactPath := flags.String("artifact", compatgate.DefaultArtifactPath, "selected Control artifact path")
	ociManifestDigest := flags.String("oci-manifest-digest", "", "selected immutable OCI image manifest digest")
	databaseEnvironment := flags.String("database-url-env", compatgate.DefaultDatabaseEnv, "environment variable containing the protected database URL")
	checkOnly := flags.Bool("check-only", false, "validate without starting the selected artifact")
	if err := flags.Parse(args); err != nil {
		os.Exit(compatgate.ExitIncompatible)
	}
	databaseURL := os.Getenv(*databaseEnvironment)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if *ociManifestDigest != "" {
		if len(commandArgs) != 0 || !*checkOnly {
			printFailure(&compatgate.Failure{Code: compatgate.ExitIncompatible, Reason: "oci_check_only_required"})
			os.Exit(compatgate.ExitIncompatible)
		}
		if _, err := compatgate.ValidateOCIDigest(ctx, *manifestPath, *publicKeyPath, *ociManifestDigest, databaseURL); err != nil {
			printFailure(err)
			os.Exit(exitCode(err))
		}
		return
	}
	if err := compatgate.EnsureCommandPath(*artifactPath); err != nil {
		printFailure(err)
		os.Exit(exitCode(err))
	}
	if err := compatgate.Run(ctx, *manifestPath, *publicKeyPath, *artifactPath, databaseURL, commandArgs, *checkOnly); err != nil {
		printFailure(err)
		os.Exit(exitCode(err))
	}
}

func printFailure(err error) {
	if err == nil {
		return
	}
	var failure *compatgate.Failure
	if asFailure(err, &failure) {
		fmt.Fprintf(os.Stderr, "relay-control-compat-gate: %s\n", failure.Reason)
		return
	}
	fmt.Fprintln(os.Stderr, "relay-control-compat-gate: preflight_failed")
}

func exitCode(err error) int {
	var failure *compatgate.Failure
	if asFailure(err, &failure) {
		return failure.Code
	}
	return compatgate.ExitIncompatible
}

func asFailure(err error, target **compatgate.Failure) bool {
	for err != nil {
		if failure, ok := err.(*compatgate.Failure); ok {
			*target = failure
			return true
		}
		type unwrapper interface{ Unwrap() error }
		unwrapped, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = unwrapped.Unwrap()
	}
	return false
}
