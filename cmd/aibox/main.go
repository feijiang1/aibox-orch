// Command aibox is the AI Box orchestration CLI. It provides docker-compose-style
// UX (up / down / ps / logs / restart / run) over a k8s/k3s-compatible blueprint
// manifest, scheduling each workload onto its isolation tier (native / container
// / kata / acrn-vm).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/intel/aibox-orch/pkg/app"
	v1 "github.com/intel/aibox-orch/pkg/apis/aibox/v1"
	"github.com/intel/aibox-orch/pkg/blueprint"
	"github.com/intel/aibox-orch/pkg/telemetry"
	"github.com/intel/aibox-orch/pkg/tier"
)

// blueprintT aliases the manifest type for terse signatures in this file.
type blueprintT = v1.Blueprint

func findWorkload(bp *blueprintT, name string) (v1.Workload, bool) {
	for _, w := range bp.Spec.Workloads {
		if w.Metadata.Name == name {
			return w, true
		}
	}
	return v1.Workload{}, false
}

const usage = `aibox - NG AI Box orchestration (k8s/k3s-compatible thin client)

Usage:
  aibox up       -f <blueprint.yaml>          Bring up the full stack (compose: up -d)
  aibox down     -f <blueprint.yaml>          Stop & remove all workloads (compose: down)
  aibox ps       -f <blueprint.yaml>          Show workload status (compose: ps)
  aibox status   -f <blueprint.yaml>          Alias of ps
  aibox restart  -f <blueprint.yaml> <name>   Restart one workload (compose: restart)
  aibox logs     -f <blueprint.yaml> <name>   Show a workload's logs (compose: logs)
  aibox run      -f <blueprint.yaml>          Up, then run the self-heal loop in foreground

Global flags:
  -f, --blueprint   path to blueprint manifest (required)
  --containerd      containerd socket address (default /run/containerd/containerd.sock)
  --simulate        use the simulated container driver (no containerd needed)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd := os.Args[1]

	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	var bpPath, cdAddr, otlpEndpoint string
	var simulate bool
	fs.StringVar(&bpPath, "f", "", "blueprint manifest path")
	fs.StringVar(&bpPath, "blueprint", "", "blueprint manifest path")
	fs.StringVar(&cdAddr, "containerd", "", "containerd socket address")
	fs.BoolVar(&simulate, "simulate", false, "use simulated container driver")
	fs.StringVar(&otlpEndpoint, "otlp", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), "OTLP gRPC endpoint for traces")
	_ = fs.Parse(os.Args[2:])

	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		fmt.Print(usage)
		return
	}
	if bpPath == "" {
		fatal("missing required -f/--blueprint flag\n\n" + usage)
	}

	bp, err := blueprint.Load(bpPath)
	if err != nil {
		fatal("load blueprint: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	a, err := app.New(app.Config{
		ContainerdAddress: cdAddr,
		SimulateContainer: simulate,
		Logger:            logger,
		PollInterval:      300 * time.Millisecond,
		ReadyTimeout:      60 * time.Second,
	})
	if err != nil {
		fatal("init: %v", err)
	}
	defer a.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	shutdown, err := telemetry.Init(ctx, telemetry.Config{Endpoint: otlpEndpoint, Insecure: true})
	if err != nil {
		fatal("telemetry init: %v", err)
	}
	defer shutdown(context.Background())

	switch cmd {
	case "up":
		doUp(ctx, a, bp)
	case "down":
		if err := a.Reconciler.Down(ctx, bp); err != nil {
			fatal("down: %v", err)
		}
		fmt.Printf("blueprint %q: all workloads stopped & removed\n", bp.Metadata.Name)
	case "ps", "status":
		doPS(ctx, a, bp)
	case "restart":
		name := firstArg(fs.Args())
		if name == "" {
			fatal("restart requires a workload name")
		}
		start := time.Now()
		if err := a.Reconciler.Restart(ctx, bp, name); err != nil {
			fatal("restart %s: %v", name, err)
		}
		fmt.Printf("restarted %q in %s\n", name, time.Since(start).Round(time.Millisecond))
	case "logs":
		name := firstArg(fs.Args())
		if name == "" {
			fatal("logs requires a workload name")
		}
		doLogs(ctx, a, bp, name)
	case "run":
		doUp(ctx, a, bp)
		fmt.Println("entering self-heal reconcile loop (Ctrl-C to exit)...")
		if err := a.Reconciler.Run(ctx, bp, a.ReconcileInterval()); err != nil && ctx.Err() == nil {
			fatal("run loop: %v", err)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
}

func doUp(ctx context.Context, a *app.App, bp *blueprintT) {
	start := time.Now()
	fmt.Printf("bringing up blueprint %q (%d workloads)...\n", bp.Metadata.Name, len(bp.Spec.Workloads))
	if err := a.Reconciler.Up(ctx, bp); err != nil {
		fatal("up: %v", err)
	}
	fmt.Printf("blueprint %q ready in %s\n", bp.Metadata.Name, time.Since(start).Round(time.Millisecond))
}

func doPS(ctx context.Context, a *app.App, bp *blueprintT) {
	states, err := a.Reconciler.Status(ctx, bp)
	if err != nil {
		fatal("status: %v", err)
	}
	names := make([]string, 0, len(bp.Spec.Workloads))
	tierOf := map[string]tier.Tier{}
	for _, w := range bp.Spec.Workloads {
		names = append(names, w.Metadata.Name)
		t, _ := tier.Resolve(w)
		tierOf[w.Metadata.Name] = t
	}
	sort.Strings(names)

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 3, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTIER\tPHASE\tMESSAGE")
	for _, n := range names {
		st := states[n]
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", n, tierOf[n], st.Phase, st.Message)
	}
	tw.Flush()
}

func doLogs(ctx context.Context, a *app.App, bp *blueprintT, name string) {
	w, ok := findWorkload(bp, name)
	if !ok {
		fatal("workload %q not found in blueprint", name)
	}
	t, err := tier.Resolve(w)
	if err != nil {
		fatal("resolve tier: %v", err)
	}
	d, ok := a.Driver(t)
	if !ok {
		fatal("no driver for tier %q", t)
	}
	rc, err := d.Logs(ctx, name, false)
	if err != nil {
		fatal("logs %s: %v", name, err)
	}
	defer rc.Close()
	_, _ = io.Copy(os.Stdout, rc)
}

func firstArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "aibox: "+format+"\n", a...)
	os.Exit(1)
}
