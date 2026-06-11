package rng

import "math"

// CertifiedFloat: float64 uniforme en [0, 1).
func CertifiedFloat(mt *MT19937) float64 {
	return mt.NextFloat()
}

// CertifiedInt: entero uniforme en [min, max] inclusive.
// Usa rejection sampling para evitar sesgo de módulo en rangos pequeños:
// se descartan valores en la "cola superior" no divisible por (max-min+1).
func CertifiedInt(mt *MT19937, min, max int) int {
	if max < min {
		panic("CertifiedInt: max < min")
	}
	rang := uint32(max - min + 1)
	if rang == 0 { // todo el espacio uint32 cubierto
		return min + int(mt.NextUint32())
	}
	// Descartar valores >= limit, donde limit es el mayor múltiplo de rang
	// que cabe en uint32. Esto garantiza distribución exactamente uniforme.
	limit := uint32(0xffffffff) - (uint32(0xffffffff) % rang) - 1
	for {
		v := mt.NextUint32()
		if v <= limit {
			return min + int(v%rang)
		}
	}
}

// CertifiedFloatRange: float en [min, max] redondeado a `decimals` decimales.
func CertifiedFloatRange(mt *MT19937, min, max float64, decimals int) float64 {
	v := min + mt.NextFloat()*(max-min)
	mul := math.Pow(10, float64(decimals))
	return math.Round(v*mul) / mul
}

// CertifiedShuffle: Fisher-Yates in-place sobre cualquier slice.
// Genérico — funciona sobre []T para cualquier T.
func CertifiedShuffle[T any](mt *MT19937, arr []T) {
	for i := len(arr) - 1; i > 0; i-- {
		j := CertifiedInt(mt, 0, i)
		arr[i], arr[j] = arr[j], arr[i]
	}
}

// CertifiedNormal: Box-Muller — devuelve sólo z0 por llamada (z1 se descarta).
// Consume dos uint32 del MT19937 por invocación.
func CertifiedNormal(mt *MT19937, mean, std float64) float64 {
	u1 := mt.NextFloat()
	if u1 == 0 {
		u1 = 1e-12 // evita log(0)
	}
	u2 := mt.NextFloat()
	z0 := math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
	return mean + std*z0
}

// CertifiedNormalClamped: normal clampada a [min, max] con hasta 50 reintentos.
// Si los 50 intentos quedan fuera de rango, devuelve el último valor saturado
// al borde más cercano (no NaN ni Inf).
func CertifiedNormalClamped(mt *MT19937, mean, std, min, max float64) float64 {
	for i := 0; i < 50; i++ {
		v := CertifiedNormal(mt, mean, std)
		if v >= min && v <= max {
			return v
		}
	}
	// Fallback: saturar el último intento al borde más cercano.
	v := CertifiedNormal(mt, mean, std)
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
