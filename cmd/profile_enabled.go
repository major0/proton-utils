//go:build profile

package cli

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
)

var profileDir string

// RegisterProfileFlag adds --profile to the root command.
func RegisterProfileFlag() {
	rootCmd.PersistentFlags().StringVar(&profileDir, "profile", "", "Write CPU, mutex, block, goroutine profiles and execution trace to this directory")
}

// StartProfile begins all profiling if --profile was set. Returns a
// cleanup function that stops profiling and writes the output files.
// Call the returned function via defer in the root PersistentPreRunE.
func StartProfile() func() {
	if profileDir == "" {
		return func() {}
	}

	if err := os.MkdirAll(profileDir, 0755); err != nil {
		slog.Error("profile: create dir", "error", err)
		return func() {}
	}

	// CPU profile.
	cpuF, err := os.Create(filepath.Join(profileDir, "cpu.prof"))
	if err != nil {
		slog.Error("profile: create cpu.prof", "error", err)
		return func() {}
	}
	if err := pprof.StartCPUProfile(cpuF); err != nil {
		cpuF.Close()
		slog.Error("profile: start cpu profile", "error", err)
		return func() {}
	}

	// Execution trace.
	traceF, err := os.Create(filepath.Join(profileDir, "trace.out"))
	if err != nil {
		pprof.StopCPUProfile()
		cpuF.Close()
		slog.Error("profile: create trace.out", "error", err)
		return func() {}
	}
	if err := trace.Start(traceF); err != nil {
		traceF.Close()
		pprof.StopCPUProfile()
		cpuF.Close()
		slog.Error("profile: start trace", "error", err)
		return func() {}
	}

	// Enable mutex and block profiling.
	runtime.SetMutexProfileFraction(1)
	runtime.SetBlockProfileRate(1)

	return func() {
		trace.Stop()
		traceF.Close()
		pprof.StopCPUProfile()
		cpuF.Close()

		writeNamedProfile("mutex")
		writeNamedProfile("block")
		writeNamedProfile("goroutine")

		slog.Info("profiles written", "dir", profileDir)
	}
}

func writeNamedProfile(name string) {
	f, err := os.Create(filepath.Join(profileDir, name+".prof"))
	if err != nil {
		slog.Error(fmt.Sprintf("profile: create %s.prof", name), "error", err)
		return
	}
	defer f.Close()
	if p := pprof.Lookup(name); p != nil {
		p.WriteTo(f, 0)
	}
}
