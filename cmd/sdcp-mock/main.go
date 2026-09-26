// Command sdcp-mock runs a simulated ELEGOO SDCP V3 printer for trying out
// the printer features without a real printer:
//
//	sdcp-mock [--http 0.0.0.0:3030] [--udp 0.0.0.0:3000] [--layers 200] [--layer-time 1s]
//
// Point stlib at it with PRINTER_ADDR=<host>[:<http port>] (and
// PRINTER_DISCOVERY_PORT if the discovery port isn't 3000).
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/codeStev/stl-library/internal/adapters/sdcp/sdcptest"
)

func main() {
	httpAddr := flag.String("http", "0.0.0.0:3030", "control channel and uploads")
	udpAddr := flag.String("udp", "0.0.0.0:3000", "discovery")
	layers := flag.Int("layers", 200, "layers of every simulated print")
	layerTime := flag.Duration("layer-time", time.Second, "time per simulated layer")
	readRate := flag.Int("read-rate", 0, "simulated network: read uploads at most this many bytes/s (0 = unlimited)")
	chunkDelay := flag.Duration("chunk-delay", 0, "simulated time the printer spends on each upload chunk")
	checkDelay := flag.Duration("check-delay", 0, "simulated re-checking: extra time per chunk per MiB received so far (Check=1)")
	finalizeDelay := flag.Duration("finalize-delay", 0, "keep a finished upload under its temporary name this long (the real printer checks it first)")
	flag.Parse()
	m := sdcptest.New()
	m.Layers, m.LayerTime = *layers, *layerTime
	m.ReadRate, m.ChunkDelay, m.CheckDelay, m.FinalizeDelay = *readRate, *chunkDelay, *checkDelay, *finalizeDelay
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := m.Run(ctx, *httpAddr, *udpAddr); err != nil {
		log.Fatal(err)
	}
}
