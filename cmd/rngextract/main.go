// Command rngextract: herramienta de extracción de datos del RNG para la
// sumisión GLI (Composite Submission Requirements v2.0 §2.2).
//
// Cubre las dos herramientas exigidas, llamando EXACTAMENTE al código de
// producción (mismo paquete rng, mismos generadores — nada reimplementado):
//
//	-mode bits   Raw Output Collection Tool: vuelca la salida CRUDA del
//	             DRBG (antes de cualquier escalado/selección) en binario.
//	             Cada NextUint32 se escribe big-endian, lo que reconstruye
//	             byte a byte el stream HMAC-DRBG. Para NIST SP 800-22,
//	             Dieharder y PractRand. GLI pide ~96 Mbit como mínimo;
//	             -bytes admite GB.
//
//	-mode game   Final Outcome Collection Tool: produce resultados FINALES
//	             de juego con el pipeline real (GenerateGame: selector IPF
//	             → finish → cuotas acopladas → bonus), incluida la
//	             transición entre rondas de producción (state modifier +
//	             reseed por frontera de ronda). Salida CSV parseable.
//
//	-mode int    Salida escalada de CertifiedInt en [min,max]: evidencia
//	             específica del rejection sampling (R8) sobre rangos no
//	             potencia de 2 (p. ej. 6 y 8). CSV de una columna.
//
// Fuente según build (idéntico contrato que cmd/race-generator):
//   - producción (por defecto): HMAC-DRBG sembrado de crypto/rand; -seed
//     prohibido.
//   - laboratorio (-tags gli_lab): -seed (64 hex) OBLIGATORIO; corrida
//     reproducible bit a bit (la evidencia de la sumisión registra la
//     semilla, la versión y el hash del binario).
//
// Toda la metadata de la corrida (fuente, modo, parámetros, versión) se
// escribe en stderr para anexarla a la evidencia; stdout/-out lleva SOLO
// los datos.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"time"

	"vg-racegen/internal/racegen/config"
	"vg-racegen/internal/racegen/data"
	"vg-racegen/internal/racegen/generators"
	"vg-racegen/internal/racegen/rng"
	"vg-racegen/internal/racegen/videoselector"
)

func main() {
	var (
		mode     = flag.String("mode", "", "bits | game | int")
		out      = flag.String("out", "", "output file (default stdout)")
		nBytes   = flag.Int64("bytes", 12_000_000, "mode bits: bytes to emit (96 Mbit = 12 MB; use >=1e9 for PractRand)")
		count    = flag.Int64("count", 1_000_000, "mode game/int: number of outcomes/draws")
		gameType = flag.String("gametype", "dog8", "mode game: dog8 | dog6 | horse_classic")
		minV     = flag.Int("min", 1, "mode int: range lower bound (inclusive)")
		maxV     = flag.Int("max", 8, "mode int: range upper bound (inclusive)")
		seedHex  = flag.String("seed", "", "gli_lab builds only: 64-hex deterministic seed")
	)
	flag.Parse()

	src, desc, err := makeSource(*seedHex)
	if err != nil {
		fatal("source: %v", err)
	}

	w := io.Writer(os.Stdout)
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fatal("create %s: %v", *out, err)
		}
		defer func() {
			if err := f.Close(); err != nil {
				fatal("close %s: %v", *out, err)
			}
		}()
		w = f
	}
	bw := bufio.NewWriterSize(w, 1<<20)

	logMeta("tool=rngextract version=%s mode=%s source=%q start=%s",
		buildVersion(), *mode, desc, time.Now().UTC().Format(time.RFC3339))

	switch *mode {
	case "bits":
		err = runBits(bw, src, *nBytes)
	case "game":
		err = runGame(bw, src, *gameType, *count)
	case "int":
		err = runInt(bw, src, *minV, *maxV, *count)
	default:
		flag.Usage()
		os.Exit(2)
	}
	if err != nil {
		fatal("%s: %v", *mode, err)
	}
	if err := bw.Flush(); err != nil {
		fatal("flush: %v", err)
	}
	logMeta("done draws=%d end=%s", src.GenerationCount(), time.Now().UTC().Format(time.RFC3339))
}

// runBits es la Raw Output Collection Tool: n bytes de salida cruda del
// Source en binario. Cada uint32 va big-endian — el mismo orden de bytes en
// que el DRBG produce su key-stream, de modo que el archivo ES el stream
// crudo pre-escalado.
func runBits(w *bufio.Writer, src rng.Source, n int64) error {
	var buf [4]byte
	for written := int64(0); written < n; written += 4 {
		v := src.NextUint32()
		buf[0] = byte(v >> 24)
		buf[1] = byte(v >> 16)
		buf[2] = byte(v >> 8)
		buf[3] = byte(v)
		if _, err := w.Write(buf[:]); err != nil {
			return err
		}
		if written > 0 && written%(256<<20) == 0 {
			logMeta("bits progress=%dMB", written>>20)
		}
	}
	return nil
}

// runGame es la Final Outcome Collection Tool: count rondas completas con
// el pipeline de producción. CSV: una fila por ronda con el resultado final
// (orden de llegada completo), el vídeo elegido, bonus y cuotas WIN.
//
// Replica la secuencia por ronda de cmd/race-generator (generateAndPersist):
// GenerateGame → ModifyStateBetweenGames → Reseed de frontera de ronda. Los
// slots avanzan por la rejilla real del gameType para que la identidad de
// ronda sea la de producción.
func runGame(w *bufio.Writer, src rng.Source, gameType string, count int64) error {
	cfg, err := config.Get(gameType)
	if err != nil {
		return err
	}
	pool := data.VideoPool(cfg.VideoPoolPath)
	if pool == nil {
		return fmt.Errorf("no video pool for %q", gameType)
	}
	sel, err := videoselector.New(pool, cfg)
	if err != nil {
		return err
	}
	jp := &generators.JackpotState{Current: generators.JackpotInitialValue}

	// Slot inicial fijo (no time.Now()): la identidad de ronda es metadata;
	// los draws no dependen del slot. Fijo ⇒ el CSV es reproducible en lab.
	slot := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	step := time.Duration(cfg.RoundIntervalSec) * time.Second

	if _, err := fmt.Fprintln(w, "idx,roundCode,videoId,first,second,order,bonus,winOdds"); err != nil {
		return err
	}
	for i := int64(0); i < count; i++ {
		g, err := generators.GenerateGame(src, cfg, sel, jp, slot, nil, nil)
		if err != nil {
			return fmt.Errorf("round %d: %w", i, err)
		}
		if _, err := rng.ModifyStateBetweenGames(src, g.RoundCode); err != nil {
			return fmt.Errorf("round %d state mod: %w", i, err)
		}
		if rs, ok := src.(rng.Reseeder); ok {
			if err := rs.Reseed([]byte(g.RoundCode)); err != nil {
				return fmt.Errorf("round %d reseed: %w", i, err)
			}
		}

		order := g.Finish.Order()
		if _, err := fmt.Fprintf(w, "%d,%s,%s,%d,%d,%s,%d,%s\n",
			i, g.RoundCode, extractVideoID(g.Finish.VideoName.MP4),
			g.Finish.First, g.Finish.Second,
			joinInts(order, "|"), g.Bonus,
			joinFloats(g.Odds[:cfg.WinOddsCount], "|")); err != nil {
			return err
		}
		slot = slot.Add(step)
		if i > 0 && i%500_000 == 0 {
			logMeta("game progress=%d/%d", i, count)
		}
	}
	return nil
}

// runInt: count draws de CertifiedInt en [min,max] — evidencia del
// rejection sampling sobre rangos arbitrarios (R8).
func runInt(w *bufio.Writer, src rng.Source, min, max int, count int64) error {
	if max < min {
		return fmt.Errorf("max(%d) < min(%d)", max, min)
	}
	for i := int64(0); i < count; i++ {
		if _, err := fmt.Fprintf(w, "%d\n", rng.CertifiedInt(src, min, max)); err != nil {
			return err
		}
	}
	return nil
}

// extractVideoID saca el token R\d+ / stem del nombre de vídeo (espejo del
// helper no exportado de generators; aquí basta el basename sin extensión).
func extractVideoID(mp4 string) string {
	base := mp4
	for i := len(base) - 1; i >= 0; i-- {
		if base[i] == '/' {
			base = base[i+1:]
			break
		}
	}
	for i := 0; i < len(base); i++ {
		if base[i] == '.' {
			return base[:i]
		}
	}
	return base
}

func joinInts(xs []int, sep string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += sep
		}
		out += fmt.Sprintf("%d", x)
	}
	return out
}

func joinFloats(xs []float64, sep string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += sep
		}
		out += fmt.Sprintf("%g", x)
	}
	return out
}

func buildVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 12 {
				return s.Value[:12]
			}
		}
	}
	return "dev"
}

func logMeta(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "rngextract: "+format+"\n", args...)
}

func fatal(format string, args ...any) {
	logMeta("FATAL "+format, args...)
	os.Exit(1)
}
