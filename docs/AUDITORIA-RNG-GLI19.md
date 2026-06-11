# Auditoría del módulo RNG — GLI-19 v3.0, Capítulo 3

| Campo | Valor |
|---|---|
| **Proyecto** | vg-racegen (race-generator) |
| **Alcance** | `internal/racegen/rng`, consumidores (`videoselector`, `generators`, `cmd/race-generator`), `internal/racegen/audit` |
| **Estándar** | GLI-19 *Standards for Interactive Gaming Systems* v3.0, Cap. 3 (RNG Requirements). Referencias cruzadas: GLI-11 v3.0 §RNG, NIST SP 800-22 Rev. 1a, SP 800-90A/B/C |
| **Tipo de revisión** | Auditoría estática (Fase 1) — solo análisis de código, sin ejecución de baterías estadísticas |
| **Fecha** | 2026-06-11 |
| **Commit auditado** | `3208049` (main) |

> **Nota de alcance:** ningún auto-test garantiza certificación; la emite el laboratorio
> (GLI/BMM) tras evaluar código fuente, documentación y datos de prueba. Este informe cubre
> los requisitos verificables internamente por revisión de código. Las baterías estadísticas
> (NIST SP 800-22, Dieharder, PractRand, chi² por símbolo) quedan **pendientes** y no deben
> ejecutarse hasta resolver los hallazgos críticos: medirían un generador que va a cambiar.

---

## 1. Resumen ejecutivo

El módulo RNG está bien construido en lo "clásico": escalado sin sesgo de módulo
(rejection sampling correcto), audit trail con cadena de hashes SHA-256, cero `math/rand`
en el camino de producción, flujo número→resultado trazable de punta a punta y concurrencia
sana. Sin embargo tiene **un problema arquitectónico de fondo** que produce tres
no-conformidades críticas:

1. La fuente primaria de todos los resultados de juego es un **MT19937** (Mersenne
   Twister), cuyo estado interno es reconstruible a partir de 624 salidas consecutivas.
   No cumple los requisitos de imprevisibilidad (R3) ni de no-recuperabilidad de
   semilla/estado (R4) de GLI-19 v3.0.
2. En producción la semilla es **determinista y obligatoria** (`RACEGEN_SEED_HEX`):
   toda la secuencia futura de carreras es función de un valor de 64 hex en texto plano
   en la configuración de despliegue (R5).
3. Esa semilla se escribe **en claro en el audit log** (entrada `init`).

La conclusión operativa: **no invertir en las baterías estadísticas todavía**. MT19937 las
pasaría todas — el problema no es estadístico, es de imprevisibilidad criptográfica. Primero
debe decidirse la estrategia de fuente (§6).

### ⚠️ Advertencia sobre guía contradictoria

Existe documentación interna/skill local (`gli-rng-certification`) que afirma que GLI
rechaza CSPRNGs del sistema y exige implementaciones "ad-hoc" tipo Mersenne Twister o
xorshift. El código parece haberse construido siguiendo esa filosofía (los comentarios
llaman al MT19937 "RNG certificado GLI-19"; `mt19937.go:77` recomienda seed determinista
"para producción"). **Esa guía es incorrecta para GLI-19 v3.0**: el estándar exige que no
sea computacionalmente viable predecir salidas futuras a partir de salidas pasadas, ni
deducir la semilla o el estado interno desde la salida — MT19937 viola ambas propiedades
por diseño (su estado se recupera de 624 salidas temperadas consecutivas; xorshift y PCG
tienen ataques análogos). La lectura correcta del estándar es la opuesta: fuente CSPRNG
(`crypto/rand`) o DRBG conforme a NIST SP 800-90A sembrado desde entropía del SO.

---

## 2. Hallazgos

### Críticos

#### H1 — MT19937 como generador primario de resultados de juego

- **Evidencia:** `internal/racegen/rng/mt19937.go` (todo el paquete); consumido por
  `videoselector/selector.go:215` (selección IPF del orden de llegada),
  `generators/odds.go` (cuotas, acoplamiento Mallows/Plackett-Luce),
  `generators/finish.go`, `generators/competitors.go`, `generators/conditions.go`.
- **Requisitos afectados:** R3, R4, R6.
- **Detalle:** todo resultado de juego deriva de un Mersenne Twister MT19937. No es un
  DRBG SP 800-90A ni lectura de `crypto/rand`. El estado interno (624 × uint32) se
  reconstruye a partir de 624 salidas consecutivas invirtiendo el tempering; a partir de
  ahí todas las salidas futuras son computables. Aunque la salida publicada (resultados de
  carrera, cuotas) está muy procesada y la recuperación práctica es más difícil que con
  salida cruda, el estándar exige imposibilidad computacional, no dificultad práctica.
- **Severidad:** crítica.
- **Parche propuesto:** ver §6.

#### H2 — Semilla determinista obligatoria en producción

- **Evidencia:** `cmd/race-generator/main.go:786-794` (fail-closed: con
  `APP_ENV ∈ {prod, staging, stg}` el binario **exige** `RACEGEN_SEED_HEX`);
  `main.go:679-701` (`makeMT`); comentario invertido en `mt19937.go:77`
  ("Para producción es preferible NewMT19937WithSeedHex con seed determinista").
- **Requisito afectado:** R5.
- **Detalle:** toda la secuencia futura de carreras es función determinista de un valor
  de 64 hex que vive en texto plano en la config de despliegue (compose, CI, entorno).
  Quien lo lea puede precomputar todos los resultados futuros. La justificación en el
  código ("GLI-19 §3.3 audit replay must be deterministic") es una interpretación
  invertida del estándar: la reproducibilidad con semilla conocida se exige en el banco
  de pruebas del laboratorio, no en la operación real. En producción el seeding debe ser
  impredecible.
- **Severidad:** crítica.
- **Parche propuesto:** producción siembra siempre desde `crypto/rand` (o el DRBG se
  auto-siembra del SO). El modo reproducible por semilla queda aislado tras build tag
  (p. ej. `//go:build gli_lab`) y excluido del binario de producción — GLI exige
  precisamente esa separación para modos de test.

#### H3 — La semilla se registra en claro en el audit log

- **Evidencia:** `cmd/race-generator/main.go:217-227` — la entrada `init` del audit
  JSONL incluye `seedHex` (archivo en disco, modo 0640, ruta `RACEGEN_AUDIT_PATH`).
- **Requisitos afectados:** R4, R5 (derivado de H2).
- **Detalle:** combinado con H2, cualquier acceso de lectura al audit log permite
  reconstruir el flujo completo de resultados pasados **y futuros**. Si la semilla debe
  conservarse para el laboratorio, debe protegerse (secreto gestionado, cifrado, HSM) y
  no compartir archivo con material operativo legible.
- **Severidad:** alta.

### Medios

#### H4 — Background cycling insuficiente como mitigación de predictibilidad

- **Evidencia:** `internal/racegen/rng/state_modifier.go:27-48`;
  `cmd/race-generator/main.go:625-642`.
- **Requisito afectado:** R7.
- **Detalle:** entre rondas se descartan entre 1 y 100 valores del MT, con conteo
  extraído de `crypto/rand` (con rejection sampling — correcto). Eso añade <log₂(100) ≈
  6.7 bits de incertidumbre por ronda: un observador que conozca el estado lo fuerza con
  100 hipótesis verificables contra el resultado publicado. Además el ciclado es por
  evento (entre rondas), no continuo en background. No sustituye la imprevisibilidad de
  la fuente; sería irrelevante si H1 se resuelve con CSPRNG (en cuyo caso documentar R7
  como N/A por diseño).
- **Severidad:** media.

#### H5 — No existe monitoreo operacional del RNG (nuevo requisito v3.0)

- **Evidencia:** ausencia de componente; `cmd/mon/main.go` es un comparador de feeds
  prod/bridge, no un health-check estadístico del RNG.
- **Requisito afectado:** R12.
- **Detalle:** GLI-19 v3.0 (§RNG Strength and Monitoring) espera monitoreo de la salud
  del RNG en operación. Falta diseñar: job en ventana móvil (p. ej. últimas 10 000
  carreras) con chi² + runs sobre la salida escalada, alerta con umbral p < 0.0001
  sostenido → pausa del juego + notificación (nunca corrección silenciosa), y registro
  firmado de resultados (la infraestructura de hash-chain de `internal/racegen/audit` es
  reutilizable para esto).
- **Severidad:** media (requisito de sumisión, no defecto del generador).

### Menores

#### H6 — `CertifiedFloat` con 32 bits de resolución

- **Evidencia:** `internal/racegen/rng/mt19937.go:108-110`
  (`float64(NextUint32()) / 2^32`).
- **Detalle:** las probabilidades del selector IPF y de Plackett-Luce quedan cuantizadas
  a múltiplos de 2⁻³². El sesgo es despreciable en la práctica (pools ≤1k entradas,
  rangos ≤8), pero el laboratorio pregunta por la resolución del mapeo. Lo estándar es
  53 bits (dos draws de 32/21 bits). Documentar o ampliar.
- **Severidad:** menor.

#### H7 — Saturación en `CertifiedNormalClamped`

- **Evidencia:** `internal/racegen/rng/certified.go:63-79` (tras 50 rechazos satura al
  borde más cercano → masa de probabilidad puntual en min/max); `certified.go:52-54`
  (`u1 == 0 → 1e-12`).
- **Detalle:** con los parámetros configurados la probabilidad de agotar 50 intentos es
  ínfima, pero es un sesgo formal que debe constar en la descripción matemática
  (R14). Sin acción de código necesaria si se documenta.
- **Severidad:** menor.

---

## 3. Matriz de cumplimiento GLI-19 v3.0 (Cap. 3)

| # | Requisito (resumen) | Estado | Evidencia / justificación |
|---|---|---|---|
| R1 | Salida estadísticamente independiente | **PENDIENTE** | Batería interna existente (`rng/statistical_test.go`); NIST/Dieharder/PractRand no ejecutados — bloqueado por H1 (no medir un generador que va a cambiar) |
| R2 | Distribución uniforme / conforme a lo declarado | **PENDIENTE** | `generators/fairness_test.go` (chi² mercado WIN) existe; falta volumen de sumisión (≥10 M resultados, ≥1 GB bits crudos) |
| R3 | Imprevisibilidad de salidas futuras | **NO CUMPLE** | H1 — MT19937 como fuente primaria |
| R4 | Semilla/estado no deducible de la salida | **NO CUMPLE** | H1 (estado recuperable de 624 salidas) + H3 (seed en claro en audit log) |
| R5 | Seeding impredecible, sin semillas fijas | **NO CUMPLE** | H2 — seed determinista obligatoria en prod vía env var; sin `time.Now()` como semilla (correcto); sin semillas hardcodeadas fuera de tests (correcto) |
| R6 | Re-seeding seguro | **NO CUMPLE** | No hay reseed: el MT corre indefinidamente desde una semilla estática; sin intervalos de reseed ni prediction resistance SP 800-90A |
| R7 | Ciclado continuo / no correlación temporal | **PARCIAL** | H4 — cycling por evento con descarte 1–100 (`state_modifier.go`); sin correlación con el instante de petición (correcto: el scheduler pre-genera por slots, no por demanda) |
| R8 | Escalado sin sesgo (unbiased scaling) | **CUMPLE** | `certified.go:13-30` — rejection sampling correcto (aritmética del límite verificada: conjunto de aceptación siempre múltiplo exacto del rango); `state_modifier.go:55-79` ídem sobre crypto/rand; Fisher-Yates correcto (`certified.go:41-46`); cero `n % rango` directo |
| R9 | Sin reutilización ni cherry-picking | **CUMPLE**¹ | Flujo trazable: `Select(mt)` → finish → intervals → odds; cada petición consume salida nueva; el fallback de odds pasa por el mismo acoplamiento (`odds.go:234-255`); sin re-muestreo condicionado al resultado |
| R10 | Periodo / espacio de estados suficiente | **CUMPLE** | 2¹⁹⁹³⁷−1 ≫ necesidades del juego; re-documentar como N/A si se migra a CSPRNG |
| R11 | Fallo seguro de la fuente de entropía | **CUMPLE** | Errores de `crypto/rand` propagados y fatales (`mt19937.go:80-82`, `state_modifier.go`); sin fallback a PRNG débil; cero `math/rand` en producción (solo menciones en comentarios) |
| R12 | RNG Strength & Monitoring (v3.0) | **NO CUMPLE** | H5 — no existe monitoreo operacional |
| R13 | Versión producción = versión testeada | **PARCIAL** | `version`/`commit` inyectados por ldflags (`main.go:74-77`), `go.sum` íntegro; falta procedimiento formal de hash SHA-256 del módulo y del binario + build reproducible documentado |
| R14 | Documentación técnica para sumisión | **NO CUMPLE** | No existe descripción matemática del RNG, diagrama de flujo ni mapeo número→símbolo como documento de sumisión |

¹ R9 requiere explicación cuidadosa en la sumisión: el acoplamiento Mallows/Plackett-Luce
(`odds.go:275-311`) asigna *valores* de cuota a slots físicos en función del orden de
llegada **ya decidido** por el selector IPF. Es legítimo — P(resultado) viene de la
distribución IPF declarada y las cuotas se presentan después preservando el multiset de
valores (overround/RTP exactos) — pero "las cuotas dependen del resultado" levanta cejas
si no se documenta con precisión que la causalidad es resultado→presentación y nunca
jugador→resultado.

---

## 4. Fortalezas a conservar

- **Audit trail** (`internal/racegen/audit/log.go`): SHA-256 encadenado, secuencia
  monotónica, `Verify` completo, append-only. Con buen criterio, `state_mod` registra solo
  el conteo de descarte (no el estado interno del MT) y `game_generated` registra hashes de
  cuotas/competidores, no números crudos (`game.go:280-294`).
- **Concurrencia:** un único MT consumido en serie desde la goroutine del scheduler
  (`main.go:94-96`); documentado y verificado — sin `go func` en el camino del RNG.
- **Sin modos demo/test** que alteren el RNG en el binario de producción; sin build tags
  ocultos; los helpers de test (`modifyStateBy`) no son API pública.
- **Consumo trazable** del RNG por el motor: selector IPF → finish → intervals →
  cuotas acopladas, con conteo de generación (`mtSeqAfter`) en el audit log.
- **Suite de tests existente:** golden vectors de seed, batería estadística interna,
  fairness del mercado WIN, paridad con la referencia DS.

---

## 5. Criterio de paso para las baterías (Fase 3, pendiente)

Cuando se ejecuten (tras resolver H1/H2): p-values dentro de [0.0001, 0.9999] con la
proporción de pasadas esperada según NIST; ningún FAIL en Dieharder (WEAK aislados se
repiten con muestra nueva); PractRand sin anomalías hasta el límite probado; chi² de
bondad de ajuste de frecuencias de victoria por competidor contra las probabilidades
IPF/Plackett-Luce declaradas (enlace R2 ↔ RTP declarado). Documentar toda re-ejecución.
Volúmenes mínimos: ≥1 GB de bits crudos; ≥10 M de resultados de juego por gameType.

---

## 6. Recomendación — estrategia de remediación

Refactor mínimo que pone en verde R3–R6 **sin tocar el motor de juego**:

1. **Sustituir la fuente del stream certificado** detrás de la API existente
   (`CertifiedInt/Float/FloatRange/Shuffle/Normal/NormalClamped` se mantienen — los
   consumidores no cambian). Dos opciones a decidir con el laboratorio:
   - *(a)* lectura directa de `crypto/rand` (CSPRNG del SO; Go ≥1.24 no falla en lectura);
   - *(b)* DRBG SP 800-90A auditable en el código (AES-CTR-DRBG o HMAC-DRBG) sembrado
     desde `crypto/rand`, con intervalos de reseed y prediction resistance documentados —
     preferible si el laboratorio quiere algoritmo con fuente visible en el repo.
2. **Aislar la reproducibilidad** (`RACEGEN_SEED_HEX` + MT19937 determinista) tras build
   tag de laboratorio, excluida del binario de producción.
3. **Dejar de escribir la semilla en claro** en el audit log de producción.
4. **Diseñar el monitoreo R12** reutilizando la infraestructura de hash-chain existente.
5. **Redactar `descripcion_tecnica_rng.md`** (R14): algoritmo, seeding, escalado
   (rejection sampling exacto de `certified.go`), mapeo número→símbolo (IPF +
   Mallows/Plackett-Luce), diagrama de flujo, resoluciones (H6) y casos borde (H7).

**Riesgo residual aun con todo en verde:** los dos puntos donde el laboratorio hará más
preguntas son (a) la convergencia de frecuencias empíricas a las probabilidades IPF
declaradas y su enlace con el RTP publicado, y (b) la justificación del acoplamiento
cuotas↔resultado (nota ¹ de la matriz). Ambos son defendibles con el diseño actual, pero
viven o mueren con la calidad de la documentación R14 y la evidencia empírica de Fase 3.

---

*Informe generado por auditoría estática de código. Reproducibilidad: todos los
hallazgos referencian `archivo:línea` sobre el commit indicado en la cabecera.*
