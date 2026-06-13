# Informe — horse_classic (betoffer 241)

**Fecha:** 2026-06-13 · **Muestra DS:** 14 804 carreras reales deduplicadas (`vgcontrol-collector-horse-classic`, mensajes `gameResult`, dedup por gameId) · **Muestra GA:** 100 000 carreras (`rngextract -mode game`, build `gli_lab`, semillas registradas) · **Método:** chi² de homogeneidad DS↔GA sobre marginales de 1.º, 2.º y multiplicadores de bonus.

---

## 1. Qué es y cómo está construido

- 7 corredores, intervalo 240 s, vídeo real de 140 s, epoch 00:01 Malta, prefijo GA sobre la numeración del vendor.
- **Pool de resultados:** 338 vídeos REALES del vendor DS donde **el nombre del fichero ES el orden de llegada** (`1237465.mp4` → pos1=1, pos2=2, …). El pairing vídeo↔resultado es correcto **por construcción** — no depende de ningún JSON intermedio, a diferencia de dog8/dog6. Verificación cruzada 338/338 contra la referencia legacy y SHA-256 del pool fijado en `data/embed.go`.
- Es, en el aspecto que GLI mira con lupa en juegos de vídeo pregrabado (que el clip mostrado coincida con el resultado pagado), **el juego mejor posicionado de los tres**.

## 2. Comparación contra DS — resultado tras la recalibración de hoy

| Métrica | Antes | Después | Veredicto |
|---|---|---|---|
| 1.º puesto (homogeneidad DS↔GA) | p = 0.40 | **p = 0.44** | ✅ indistinguible (ya lo era) |
| 2.º puesto | **p = 0.006** ❌ | **p = 0.92** | ✅ indistinguible |
| Bonus x1/x2/x3 | p ≈ 0.11 (3x desviado) | **p = 0.96** | ✅ indistinguible |

### El hallazgo del 2.º puesto

El target configurado era uniforme (14.29%/box), pero **el DS real no es plano en el 2.º puesto**: su box 5 hace segundo el **15.29%** de las veces (chi² del propio DS contra uniforme: p = 0.017 sobre 14.8k carreras). Con 100k GA la diferencia se volvía detectable (p = 0.006). Recalibrado `TargetSecondPlace` a la marginal DS medida:

| Box | DS 2.º % | GA 2.º % (recalibrado) |
|---|---|---|
| 1 | 14.54 | 14.46 |
| 2 | 14.27 | 14.31 |
| 3 | 13.90 | 13.91 |
| 4 | 14.06 | 14.09 |
| **5** | **15.29** | **15.16** |
| 6 | 13.86 | 14.23 |
| 7 | 14.10 | 13.86 |

⚠️ **Advertencia de muestra:** la marginal del 2.º descansa en 14.8k carreras (vs 47k de dog8). El exceso del box 5 es +2.8σ — probablemente estructura real del pool del vendor, pero conviene **re-medir cuando la captura acumule más datos** (la nota está en el comentario del config).

### Multiplicadores (bonus)

Los valores anteriores (2x = 4.7%, 3x = 0.86%) eran las tasas de dogs usadas como placeholder. DS horse juega **2x = 4.61%, 3x = 0.71%** — el 3x estaba un 21% alto. Recalibrado a 0.046/0.0071; GA ahora reproduce 4.50%/0.70% (p = 0.96).

## 3. Limitación conocida: las CUOTAS son placeholder

El propio config lo documenta (`extended.go:742-765`): la calibración de cuotas de horse_classic es **smoke-level, no paridad DS**:

- No existe en Elastic una tabla de marginales de cuotas por rank para el betoffer 241 (doc 08 lo lista como "mixed field" sin tabla por box).
- `RankGap` y `ForecastRank` están **desactivados** (sin referencia DS de tie-rate ni tilt de exactas para 241).
- El acoplamiento cuotas↔resultado usa Mallows con Theta modesto, no el Plackett-Luce calibrado de dogs.

Consecuencia operativa que ya estaba declarada y este informe ratifica: **el gate de salida a real para betoffer 241 debe seguir cerrado en lo que respecta a cuotas** hasta hacer el "camino paridad real" (medir las marginales de cuotas de 241 en los `gameRound` de Elastic y calibrar RankGap/ForecastRank/PL como se hizo con dogs). La superficie certificable RNG (selección de vídeo sobre pool real + escalado) está intacta y ahora también DS-matched en resultados.

## 4. Estado global de los tres juegos tras la recalibración

| Juego | 1.º | 2.º | Bonus | Muestra DS |
|---|---|---|---|---|
| dog8 | p = 0.65 | p = 0.25 | p = 0.93 | 47 332 |
| dog6 | p = 0.23 | p = 0.27 | p = 0.86 | 46 516 |
| horse_classic | p = 0.44 | p = 0.92 | p = 0.96 | 14 804 |

Ninguna métrica distingue GA de DS a ningún nivel de significancia razonable (todas p > 0.2). Cambios aplicados hoy: dog8 `Target{First,Second}Place` → uniforme (DS juega plano: p = 0.49/0.29 sobre 47k); horse `TargetSecondPlace` → marginal DS medida; horse bonus → tasas DS medidas. dog6 no necesitó cambios.

## 5. Pendientes específicos de horse_classic

1. **Cuotas — camino "paridad real"** (la limitación del §3): medir marginales de cuotas 241 en Elastic y calibrar. Bloquea apertura a real del betoffer 241, no la certificación del RNG.
2. **Re-medir el 2.º puesto** cuando la captura supere ~40k carreras, y ajustar el target si el box 5 regresa a la media.
3. Los vídeos reales deben estar desplegados bajo `/.local/horse_classic/` (symlink a `DSVideo/horse/*.mp4`) — requisito de deploy, documentado en config.

---

*Reproducibilidad: muestras GA generadas con `rngextract` (gli_lab) con semillas registradas en la metadata de cada corrida; consultas DS por runtime fields sobre `vgcontrol-collector-horse-classic` con dedup por gameId.*
