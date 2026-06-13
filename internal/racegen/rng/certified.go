package rng

import "math"

// CertifiedFloat: float64 uniforme en [0, 1) con 53 bits de resolución.
//
// Consume DOS uint32 (alto primero) y usa los 53 bits superiores del par —
// la resolución completa de la mantisa float64. La variante anterior de 32
// bits cuantizaba las probabilidades del selector IPF y de Plackett-Luce a
// múltiplos de 2^-32 (hallazgo H6, docs/AUDITORIA-RNG-GLI19.md); con 53
// bits la cuantización queda por debajo de cualquier umbral observable.
func CertifiedFloat(src Source) float64 {
	hi := uint64(src.NextUint32())
	lo := uint64(src.NextUint32())
	return float64((hi<<32|lo)>>11) / (1 << 53)
}

// CertifiedInt: entero uniforme en [min, max] inclusive.
// Usa rejection sampling para evitar sesgo de módulo en rangos pequeños:
// se descartan valores en la "cola superior" no divisible por (max-min+1).
func CertifiedInt(src Source, min, max int) int {
	if max < min {
		panic("CertifiedInt: max < min")
	}
	rang := uint32(max - min + 1)
	if rang == 0 { // todo el espacio uint32 cubierto
		return min + int(src.NextUint32())
	}
	// Descartar valores >= limit, donde limit es el mayor múltiplo de rang
	// que cabe en uint32. Esto garantiza distribución exactamente uniforme.
	limit := uint32(0xffffffff) - (uint32(0xffffffff) % rang) - 1
	for {
		v := src.NextUint32()
		if v <= limit {
			return min + int(v%rang)
		}
	}
}

// CertifiedFloatRange: float en [min, max] redondeado a `decimals` decimales.
func CertifiedFloatRange(src Source, min, max float64, decimals int) float64 {
	v := min + CertifiedFloat(src)*(max-min)
	mul := math.Pow(10, float64(decimals))
	return math.Round(v*mul) / mul
}

// CertifiedShuffle: Fisher-Yates in-place sobre cualquier slice.
// Genérico — funciona sobre []T para cualquier T.
func CertifiedShuffle[T any](src Source, arr []T) {
	for i := len(arr) - 1; i > 0; i-- {
		j := CertifiedInt(src, 0, i)
		arr[i], arr[j] = arr[j], arr[i]
	}
}

// CertifiedNormal: Box-Muller — devuelve sólo z0 por llamada (z1 se descarta).
// Consume dos CertifiedFloat = CUATRO uint32 del Source por invocación.
func CertifiedNormal(src Source, mean, std float64) float64 {
	u1 := CertifiedFloat(src)
	if u1 == 0 {
		u1 = 1e-12 // evita log(0)
	}
	u2 := CertifiedFloat(src)
	z0 := math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
	return mean + std*z0
}

// CertifiedNormalClamped: normal clampada a [min, max] con hasta 50 reintentos.
// Si los 50 intentos quedan fuera de rango, devuelve el último valor saturado
// al borde más cercano (no NaN ni Inf).
func CertifiedNormalClamped(src Source, mean, std, min, max float64) float64 {
	for i := 0; i < 50; i++ {
		v := CertifiedNormal(src, mean, std)
		if v >= min && v <= max {
			return v
		}
	}
	// Fallback: saturar el último intento al borde más cercano.
	v := CertifiedNormal(src, mean, std)
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
