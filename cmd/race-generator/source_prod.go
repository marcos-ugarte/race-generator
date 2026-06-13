//go:build !gli_lab

package main

import (
	"fmt"

	"vg-racegen/internal/racegen/rng"
)

// makeSource (build de PRODUCCIÓN) construye la fuente certificada:
// HMAC-DRBG SHA-256 (SP 800-90A) sembrado del CSPRNG del sistema operativo
// (rng.NewOSStream — construcción única compartida con cmd/rngextract).
//
// El seeding de producción es SIEMPRE no determinista (GLI-19: la semilla
// debe ser impredecible; una corrida reproducible permitiría precomputar
// resultados). Por eso una RACEGEN_SEED_HEX presente es un ERROR fatal aquí
// — la reproducibilidad con semilla conocida existe solo en el build de
// laboratorio (-tags gli_lab), que nunca se despliega.
//
// Devuelve rng.CertifiedStream: el reseed por frontera de ronda es una
// garantía de TIPO, no un type-assert opcional en tiempo de ejecución.
func makeSource(seedHex string) (rng.CertifiedStream, string, error) {
	if seedHex != "" {
		return nil, "", fmt.Errorf(
			"RACEGEN_SEED_HEX is set but this is a production build: " +
				"deterministic seeding is only available under -tags gli_lab " +
				"(GLI-19 ch.3 — production seeding must be unpredictable)")
	}
	return rng.NewOSStream()
}
