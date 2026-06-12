// HMAC-DRBG con SHA-256 conforme a NIST SP 800-90A Rev. 1 §10.1.2.
//
// Es la fuente de producción del stream certificado (GLI-19 v3.0 cap. 3):
//
//   - Imprevisibilidad (R3) y no-recuperabilidad de estado (R4): propiedades
//     criptográficas del DRBG; conocer salidas pasadas no permite computar
//     futuras ni recuperar K/V.
//   - Seeding (R5): instanciación con 48 bytes (32 entropy + 16 nonce) de la
//     EntropySource inyectada — en producción crypto/rand (CSPRNG del SO).
//   - Reseed (R6): disparadores DETERMINISTAS únicamente — automático cada
//     drbgReseedInterval peticiones de generación, y explícito (Reseed) en
//     cada frontera de ronda desde el orquestador. Sin disparadores por
//     reloj: el replay del modo laboratorio debe ser función exclusiva de la
//     semilla (docs/PLAN-CERTIFICACION-GLI19.md D1.c).
//   - Fail-safe (R11): un fallo de la EntropySource detiene la generación
//     (panic) — jamás se degrada a un PRNG débil.
//
// Decisiones de implementación documentadas para la descripción técnica R14:
//
//   - drbgChunk bytes por petición interna de generación (≤ 2^16 bytes, muy
//     por debajo del máximo del estándar, 2^19 bits por petición).
//   - Sin additional_input en Generate (la actualización post-generación
//     HMAC_DRBG_Update se ejecuta igualmente, rama provided_data vacía,
//     como exige §10.1.2.5 paso 6).
//   - Sin prediction_resistance_request por petición: la resistencia a
//     predicción se obtiene con el reseed explícito por frontera de ronda.
package rng

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
)

const (
	// drbgSeedLen: 32 bytes de entropy_input (security strength 256 bits)
	// + 16 bytes de nonce (≥ securityStrength/2), per §8.6.7.
	drbgEntropyLen = 32
	drbgNonceLen   = 16

	// drbgReseedInterval: peticiones de generación entre reseeds
	// automáticos. Determinista a propósito (sin reloj). El estándar
	// permite hasta 2^48; usamos un intervalo corto porque el coste de
	// reseed es despreciable frente a la duración de una ronda (240 s).
	drbgReseedInterval = 10_000

	// drbgChunk: bytes producidos por cada petición interna de generación.
	// Cada NextUint32 consume 4 bytes del buffer; un chunk cubre 256
	// draws antes de la siguiente petición.
	drbgChunk = 1024
)

// HMACDRBG implementa Source sobre HMAC_DRBG-SHA256 (SP 800-90A §10.1.2).
//
// NO es thread-safe (mismo contrato que el resto de Sources): el scheduler
// consume en serie desde una única goroutine.
type HMACDRBG struct {
	key [sha256.Size]byte // K
	v   [sha256.Size]byte // V

	es            EntropySource
	reseedCounter uint64 // peticiones de generación desde el último (re)seed
	reseeds       uint64 // número de reseeds realizados (audit)
	gen           uint64 // uint32 emitidos (GenerationCount)

	buf  [drbgChunk]byte
	bufN int // bytes válidos restantes en buf (se consume desde el final)
}

// NewHMACDRBG instancia el DRBG (§10.1.2.3) leyendo 48 bytes de la
// EntropySource. personalization es opcional (puede ser nil) y NO necesita
// ser secreta — identifica la instancia (p.ej. "vg-racegen/race-generator/v1").
func NewHMACDRBG(es EntropySource, personalization []byte) (*HMACDRBG, error) {
	if es == nil {
		return nil, fmt.Errorf("rng: nil EntropySource")
	}
	seed := make([]byte, drbgEntropyLen+drbgNonceLen)
	if err := es.Entropy(seed); err != nil {
		return nil, fmt.Errorf("rng: instantiate entropy: %w", err)
	}
	d := &HMACDRBG{es: es}
	// K = 0x00...00, V = 0x01...01 (§10.1.2.3 paso 2-3).
	for i := range d.v {
		d.v[i] = 0x01
	}
	d.update(append(seed, personalization...))
	d.reseedCounter = 1
	return d, nil
}

// Reseed re-siembra el DRBG (§10.1.2.4) con 32 bytes frescos de la
// EntropySource. additional es opcional (puede ser nil). El orquestador lo
// invoca en cada frontera de ronda (background cycling GLI-19 §3.2.6 con
// prediction resistance real, en sustitución del descarte de valores).
func (d *HMACDRBG) Reseed(additional []byte) error {
	entropy := make([]byte, drbgEntropyLen)
	if err := d.es.Entropy(entropy); err != nil {
		return fmt.Errorf("rng: reseed entropy: %w", err)
	}
	d.update(append(entropy, additional...))
	d.reseedCounter = 1
	d.reseeds++
	d.bufN = 0 // descarta salida pre-generada bajo el estado anterior
	return nil
}

// NextUint32 implementa Source. Ante un fallo de entropía en un reseed
// automático hace panic: GLI-19 R11 exige detener el juego antes que
// degradar la fuente (con CryptoEntropy sobre Go ≥1.24 es inalcanzable).
func (d *HMACDRBG) NextUint32() uint32 {
	if d.bufN < 4 {
		if err := d.generate(); err != nil {
			panic(fmt.Sprintf("rng: HMACDRBG generate: %v", err))
		}
	}
	off := drbgChunk - d.bufN
	v := uint32(d.buf[off])<<24 | uint32(d.buf[off+1])<<16 |
		uint32(d.buf[off+2])<<8 | uint32(d.buf[off+3])
	d.bufN -= 4
	d.gen++
	return v
}

// GenerationCount implementa Source.
func (d *HMACDRBG) GenerationCount() uint64 { return d.gen }

// ReseedCount devuelve cuántos reseeds (automáticos + explícitos) se han
// ejecutado desde la instanciación. Para el audit log.
func (d *HMACDRBG) ReseedCount() uint64 { return d.reseeds }

// generate rellena el buffer interno con drbgChunk bytes frescos.
func (d *HMACDRBG) generate() error {
	if err := d.generateInto(d.buf[:]); err != nil {
		return err
	}
	d.bufN = drbgChunk
	return nil
}

// generateInto es HMAC_DRBG_Generate (§10.1.2.5) sin additional_input para
// una petición de exactamente len(dst) bytes. El estado resultante depende
// del tamaño de la petición (V itera una vez por bloque de 32 bytes), por lo
// que los known-answer tests CAVP la invocan con el tamaño del vector (128
// bytes) mientras producción usa drbgChunk.
func (d *HMACDRBG) generateInto(dst []byte) error {
	// Paso 1: reseed automático si se agotó el intervalo.
	if d.reseedCounter > drbgReseedInterval {
		if err := d.Reseed(nil); err != nil {
			return err
		}
	}
	// Pasos 3-4: temp = temp || (V = HMAC(K, V)) hasta cubrir la petición.
	for off := 0; off < len(dst); off += sha256.Size {
		d.v = hmacSum(d.key[:], d.v[:])
		copy(dst[off:], d.v[:])
	}
	// Paso 6: HMAC_DRBG_Update(additional_input=null) — se ejecuta SIEMPRE.
	d.update(nil)
	// Paso 7.
	d.reseedCounter++
	return nil
}

// update es HMAC_DRBG_Update (§10.1.2.2).
func (d *HMACDRBG) update(providedData []byte) {
	// K = HMAC(K, V || 0x00 || provided_data); V = HMAC(K, V).
	d.key = hmacSum(d.key[:], d.v[:], []byte{0x00}, providedData)
	d.v = hmacSum(d.key[:], d.v[:])
	if len(providedData) == 0 {
		return
	}
	// K = HMAC(K, V || 0x01 || provided_data); V = HMAC(K, V).
	d.key = hmacSum(d.key[:], d.v[:], []byte{0x01}, providedData)
	d.v = hmacSum(d.key[:], d.v[:])
}

// hmacSum calcula HMAC-SHA256(key, concat(parts...)).
func hmacSum(key []byte, parts ...[]byte) [sha256.Size]byte {
	mac := hmac.New(sha256.New, key)
	for _, p := range parts {
		mac.Write(p)
	}
	var out [sha256.Size]byte
	mac.Sum(out[:0])
	return out
}
