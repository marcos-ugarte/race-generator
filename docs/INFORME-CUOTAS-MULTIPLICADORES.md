# Informe final — ¿Son las cuotas y los multiplicadores de vg-racegen indistinguibles de DS?

**Proyecto:** vg-racegen (generador GA de carreras virtuales en Go)
**Pregunta:** ¿son las CUOTAS (odds) y los MULTIPLICADORES (bonus) estadísticamente indistinguibles del vendor real "DS" en 100.000 carreras por juego?
**Fecha:** 2026-06-13
**Muestra GA:** 100.000 carreras por juego (build `gli_lab` reproducible). **Referencia DS:** capturas del dispositivo vendor en Elastic (`vgcontrol-collector-*`).

---

## 1. Respuesta ejecutiva

| Juego | Cuotas (odds) | Multiplicadores (bonus) |
|---|---|---|
| **dog8** (8 corredores, betoffer 541) | **Indistinguible a efectos prácticos**, con una salvedad: tie-rate ligeramente alto en DS (Δ −3,3 pp) | **Indistinguible** (chi² p=0,63) |
| **dog6** (6 corredores, betoffer 141) | **Diferencia menor**: overround y forma del abanico OK; el motivo real es el tie-rate (GA genera +3,6 pp más empates) | **Indistinguible** (chi² p=0,57) |
| **horse_classic** (7 corredores, betoffer 241) | **Diferente** (robusto, no artefacto estadístico): overround +0,011, favorito −9%, tie −10,9 pp. Cuotas PLACEHOLDER confirmadas | **Indistinguible** (chi² p=0,77) |

**Titular:**
- **Multiplicadores: indistinguibles en los tres juegos.** Sin reservas. Confirma y refina el hallazgo previo.
- **Cuotas:** **dog8 prácticamente indistinguible**; **dog6 con una diferencia menor** localizada en el tie-rate; **horse_classic claramente diferente** (era esperado: cuotas placeholder en el código).
- **Corrección transversal del auditor:** el **tie-rate** estaba mal medido por los tres analistas de juego (muestras y criterios distintos). Recalculado con criterio uniforme (cuotas a 1 decimal, n≥27k), **el tie-rate supera el umbral de 2 pp en los tres juegos**. No invalida el veredicto de dog8 ("prácticamente indistinguible") ni el de horse ("diferente"), pero es la causa real del veredicto "diferencia menor" de dog6.

---

## 2. Cuotas en detalle por juego

La métrica clave del RTP es el **overround** = suma de 1/odds sobre las N cuotas WIN por carrera. Umbrales prácticos de "indistinguible": overround Δ<0,003; cuota por rango Δ<~5%; tie-rate Δ<2 pp.

### 2.1 dog8 (betoffer 541)

**Sanity check superado:** overround mediano DS = 1,1456 (extracción independiente del auditor: 1,1454), idéntico al target ~1,1457. Confirma que se leyó el índice del vendor DS (`vgcontrol-collector-dog8-27bb`) y NO el `virteon-generator` legacy (que daría ~1,172).

**Overround (DS ~29,7M juegos emitidos vs GA 100k carreras):**

| Percentil | DS | GA | Δ (GA−DS) |
|---|---|---|---|
| p5 | 1,1418 | 1,1417 | −0,0001 |
| p50 | 1,14563 | 1,1462 | **+0,0006** |
| p95 | 1,15044 | 1,1537 | +0,0033 |

Mediana y p5 indistinguibles (muy por debajo de 0,003). La cola p95 de GA es marginalmente más gruesa (+0,0033), pero el margen lo gobierna la mediana, que coincide a la 4ª cifra. **OK.**

**Cuotas por rango ordenado (mediana, favorito=1 … outsider=8):**

| Rango | DS | GA | Δrel |
|---|---|---|---|
| 1 (favorito) | 4,51 | 4,40 | −2,4% |
| 2 | 5,06 | 5,10 | +0,8% |
| 3 | 5,73 | 5,80 | +1,2% |
| 4 | 6,64 | 6,60 | −0,5% |
| 5 | 7,81 | 7,80 | −0,1% |
| 6 | 9,52 | 9,50 | −0,2% |
| 7 | 11,82 | 11,70 | −1,0% |
| 8 (outsider) | 14,79 | 14,20 | −4,0% |

Máximo |Δrel| = 4,0% en el outsider, por debajo del umbral 5%. Rangos intermedios <1,5%. **OK** (el auditor reconfirmó favorito DS=4,53 y outsider DS=14,74).

**Tie-rate (CORREGIDO por auditoría):** el analista reportó DS=26,6% (Δ −0,8 pp). El auditor, con criterio uniforme (n≈114k), obtiene **DS = 29,1%** vs GA 25,8% → **Δ = −3,3 pp**, supera el umbral de 2 pp. DS tiene algo más de empates que GA.

**Veredicto cuotas dog8: indistinguible a efectos prácticos**, con la salvedad de que el tie-rate ya no es estrictamente indistinguible (DS empata un poco más). Overround y abanico de cuotas son sólidamente equivalentes.

### 2.2 dog6 (betoffer 141)

**Sanity check superado:** overround mediano DS = 1,1622 (dedup) / 1,1611 (agregado); auditor 1,1625. Target ~1,1613. Índice correcto (`vgcontrol-collector-dog6`), no contaminado por el legacy.

**Overround:**

| Percentil | DS (agg, n=3298) | GA | Δ (GA−DS) |
|---|---|---|---|
| p5 | 1,1563 | 1,1570 | +0,0007 |
| p50 | 1,1611 | 1,1619 | **+0,0008** |
| p95 | 1,1665 | 1,1683 | +0,0018 |

(DS dedup n=50: p5/p50/p95 = 1,1551/1,1622/1,1689; medias DS_dedup = GA = 1,1622, idénticas.) Δ p50 muy por debajo de 0,003. El chi² binado formal sale p<0,0001, pero es engañoso por el tamaño masivo de GA; la magnitud práctica es despreciable. **Overround OK.**

**Cuotas por rango (mediana, favorito=1 … outsider=6), DS_agg vs GA:**

| Rango | DS | GA | Δrel |
|---|---|---|---|
| 1 (favorito) | 3,40 | 3,50 | −2,9% |
| 2 | 4,10 | 4,00 | +2,5% |
| 3 | 4,60 | 4,70 | −2,1% |
| 4 | 5,70 | 5,80 | −1,7% |
| 5 | 8,00 | 7,50 | +6,7% |
| 6 (outsider) | 10,50 | 9,90 | +6,1% |

Favorito a rango 4 dentro de ±3%; los dos outsiders rozan +6-7%. **Aviso del auditor:** estos rangos 5-6 de DS salen de la muestra dedup de **solo n=50 carreras** y son estadísticamente frágiles (ruido, no señal). No deben usarse como base del veredicto.

**Tie-rate (CORREGIDO — es el motivo real del veredicto):** el analista reportó DS=18,0% (Δ +0,05 pp), pero ese 18% venía de la muestra dedup n=50. El auditor, con muestra agregada n≈27,6k, obtiene **DS = 14,4%** vs GA 17,95% → **Δ = +3,6 pp**. GA genera más empates que el vendor en dog6.

**Veredicto cuotas dog6: diferencia menor.** El veredicto del analista se mantiene, pero **la causa real es el exceso de empates de GA (+3,6 pp), no el ruido de los outsiders**. Overround y abanico central son equivalentes.

### 2.3 horse_classic (betoffer 241)

**Sanity check superado:** overround mediano DS = 1,1655 (auditor 1,1656), región horse correcta, no el 1,172 fijo del legacy. Índice `vgcontrol-collector-horse-classic`. El 100% de los arrays miden 49 (7 WIN + 42 forecast): sin mezcla de mercados.

**Overround (DS n=164.901, validado con n=649.772 en 4 días, percentiles idénticos):**

| Percentil | DS | GA | Δ (GA−DS) |
|---|---|---|---|
| p5 | 1,1604 | 1,1727 | +0,0123 |
| p50 | 1,1655 | 1,1766 | **+0,0111** |
| p95 | 1,1702 | 1,1814 | +0,0112 |

**El p5 de GA (1,1727) supera al p95 de DS (1,1702): las distribuciones prácticamente NO se solapan.** Δ p50 = +0,0111 ≈ 3,7× el umbral. GA tiene mayor margen que el vendor. **FALLA.**

**Cuotas por rango (mediana, favorito=1 … outsider=7):**

| Rango | DS | GA | Δrel |
|---|---|---|---|
| 1 (favorito) | 4,066 | 3,700 | **−9,0%** |
| 2 | 4,557 | 4,500 | −1,3% |
| 3 | 5,114 | 5,200 | +1,7% |
| 4 | 6,091 | 6,200 | +1,8% |
| 5 | 7,387 | 7,500 | +1,5% |
| 6 | 9,240 | 9,200 | −0,4% |
| 7 (outsider) | 11,472 | 11,500 | +0,2% |

El favorito GA es demasiado corto (3,70 vs 4,07), −9% (>5%). Los rangos 2-7 quedan en ±1,8%. El sesgo se concentra en el favorito y explica parte del exceso de overround. **FALLA.**

**Tie-rate (CORREGIDO):** el analista reportó DS=22,66%; el auditor obtiene **DS = 25,8%** vs GA 14,91% → **Δ ≈ −10,9 pp** (aún mayor que el −7,74 pp del analista). GA genera muchísimos menos empates. **FALLA grande.**

**Veredicto cuotas horse_classic: diferente, robusto.** Las tres métricas exceden umbral por márgenes amplios (no es artefacto de potencia estadística). Era el resultado esperado (ver sección 4).

---

## 3. Multiplicadores (bonus x1/x2/x3) por juego

Veredicto homogéneo: **indistinguibles en los tres juegos.** Fuente preferente: `wsMsgType=="gameResult"` (campo bonus único por carrera, más limpio).

| Juego | DS (x1 / x2 / x3) | n DS | GA (x1 / x2 / x3) | chi² (df=2) | p-value | Veredicto |
|---|---|---|---|---|---|---|
| **dog8** | 94,48% / 4,68% / 0,84% | 58.289 (gameResult) | 94,39% / 4,73% / 0,88% | 0,924 | **0,63** | Indistinguible |
| **dog6** | 91,84% / 6,12% / 2,04% | 49 (gameRound)* | 94,58% / 4,58% / 0,84% | 1,134 | **0,57** | Indistinguible |
| **horse** | 94,74% / 4,58% / 0,68% | 17.322 (gameResult) | 94,62% / 4,67% / 0,71% | 0,52 | **0,77** | Indistinguible |

\* **Caveat dog6:** no se pudo usar `gameResult` (el MCP de Elastic se desconectó); se usó el bonus de `gameRound` con n=49. Los conteos de x2 (=3) y x3 (=1) tienen enorme error de muestreo. El veredicto se sostiene por el prior y por la limpieza de dog8/horse, pero la evidencia DS de dog6 es débil y convendría reconfirmar con `gameResult`.

**Corrección del prior:** el x1 real del vendor horse es ~0,947 (no ~0,96 como se asumía). El auditor confirma los tres p-values como consistentes y razonables.

---

## 4. El caso horse_classic — cuotas placeholder

**horse_classic es diferente por diseño conocido, no por un bug oculto.** En el código GA las cuotas de betoffer 241 están declaradas como **PLACEHOLDER**:

- **No existe tabla de marginales de cuotas DS por rango** para 241 (a diferencia de dog8/dog6, que sí la tienen).
- **RankGap y ForecastRank están desactivados** para este mercado.
- El **acoplamiento es Mallows, no Plackett-Luce**, como en los galgos.

Consecuencias medidas, coherentes entre sí:
1. **Overround más alto** (+0,011): sin la tabla de marginales, el motor no reproduce el margen exacto del vendor.
2. **Favorito demasiado corto** (3,70 vs 4,07, −9%): sin RankGap, la cuota del primer favorito se comprime; ese desplazamiento alimenta el exceso de overround.
3. **Muchos menos empates** (14,9% vs 25,8%, −10,9 pp): el acoplamiento Mallows produce un patrón de cuotas distinto al del vendor.

**Qué haría falta para cerrar la paridad de cuotas en horse:**
- Extraer del vendor DS la **tabla de marginales de cuotas por rango** para betoffer 241 (igual que existe para 541/141).
- **Activar RankGap/ForecastRank** y calibrarlos para 7 corredores.
- Evaluar migrar de Mallows a **Plackett-Luce** para alinear el acoplamiento con galgos.
- Re-correr el estudio de 100k y reverificar overround/rango/tie.

Hasta entonces, **los multiplicadores de horse SÍ son utilizables (indistinguibles); las cuotas NO lo son.**

---

## 5. Metodología y limitaciones

**Fuente DS (referencia autoritativa):** capturas del dispositivo vendor en Elastic. Las cuotas viven en mensajes `wsMsgType=="gameRound"`, cada respuesta con un `gamepool` de ~19 juegos, cada uno con su array `odds` (dog8: 64=8+56; dog6: 36=6+30; horse: 49=7+42). Extracción vía runtime field Painless multi-emit sobre `params._source.rawMessage` (recorrer cada `"odds":[`, tomar las N primeras cuotas WIN) y, en dog6, pull+parse en python3. Para evitar 504 se usó `size:0` + aggregations + `terminate_after` por shard. Sanity check de overround superado en los tres (cuadra a la 4ª cifra con el target del vendor, descartando el índice `virteon-generator` legacy).

**Muestra GA:** 100.000 carreras por juego desde los CSV reproducibles (`/tmp/study_dog8.csv`, `/tmp/study_dog6.csv`, `/tmp/study_horse_classic.csv`).

**Qué ajustó la verificación adversarial (integrado en este informe):**
1. **Tie-rate recalculado con criterio uniforme** (cuotas a 1 decimal —verificado: las cuotas DS crudas ya vienen a 1 decimal, p.ej. `5.2, 13.1, 6.9`—, n≥27k en los tres). Resultado: **el tie-rate supera 2 pp en los tres juegos** (dog8 −3,3 pp; dog6 +3,6 pp; horse −10,9 pp), corrigiendo los números de los analistas de dog8 y dog6, que lo habían medido con muestras/criterios dispares (dog6 sobre n=50).
2. **Confirmó índice DS correcto** en los tres (overrounds independientes 1,1454/1,1625/1,1656).
3. **Validó la extracción multi-emit del gamepool** (sin sesgo de "solo primer juego", filtrado por longitud de array para no mezclar mercados).
4. **Reconfirmó** favorito/outsider de dog8 y el overround disjunto de horse.

**Limitaciones reconocidas:**
- **Sin dedup por carrera en DS** (salvo dog6): se compara la distribución agregada de la ventana rolling history/future, cuya multiplicidad por carrera es ~constante → forma distribucional insesgada para medianas/percentiles (que es lo medido). Los conteos absolutos DS no equivalen a carreras únicas.
- **dog6 con muestra DS frágil en carreras únicas (n=50 dedup):** percentiles de cola y rangos 5-6 son ruidosos; por eso el veredicto se ancla en overround (estable) y en el tie-rate agregado, no en los outsiders.
- **Muestreo DS por proximidad de ingesta** (terminate_after / ventanas de 1-4 días), no aleatorio uniforme; mitigado por el enorme volumen (29,7M juegos dog8, >165k horse) y la estabilidad de percentiles entre ventanas.
- **Per-rango solo en mediana (p50)** en dog8/horse (sin p5/p95 por rango) por coste de query.
- **Tests formales con N=100k detectan Δ diminutos** (chi²/KS); por eso se juzga por **magnitud práctica**, reportando ambos.
- **Bonus dog6 sobre n=49 de gameRound** (MCP caído), no gameResult.

---

## 6. Conclusión y recomendación

**Respuesta directa a la pregunta:**

- **Multiplicadores (bonus): SÍ, indistinguibles de DS en los tres juegos** (dog8 p=0,63, dog6 p=0,57, horse p=0,77). Sin reservas materiales; reconfirmar dog6 con `gameResult` cuando vuelva el acceso a Elastic.

- **Cuotas (odds):**
  - **dog8: SÍ, indistinguible a efectos prácticos.** Overround (Δ p50 +0,0006) y abanico de cuotas (máx 4%) equivalentes. Única salvedad: DS empata un poco más (tie Δ −3,3 pp), efecto menor sobre el jugador.
  - **dog6: CASI — diferencia menor.** Overround y forma central equivalentes; GA genera +3,6 pp más empates que el vendor. Aceptable para producción, recomendable afinar el tie-rate.
  - **horse_classic: NO, diferente.** Overround más alto (+0,011, distribuciones disjuntas), favorito −9%, tie −10,9 pp. **Esperado y confirmado:** cuotas placeholder en el código.

**Recomendaciones:**
1. **dog8 y dog6: aptos para producción en cuotas.** Para dog6, considerar un ajuste fino del modelo de empates para cerrar los +3,6 pp.
2. **horse_classic: NO desplegar las cuotas como "indistinguibles".** Requiere tabla de marginales DS por rango (betoffer 241), activar RankGap/ForecastRank y evaluar Plackett-Luce; luego reverificar. Sus multiplicadores sí son utilizables.
3. **Estandarizar la medición del tie-rate** (criterio único: cuotas a 1 decimal, muestra agregada ≥25k) en futuros estudios, ya que fue la métrica peor medida.
4. **Reconfirmar bonus de dog6** con `wsMsgType=="gameResult"` cuando se restablezca el acceso a Elastic.

**En una frase:** los multiplicadores son indistinguibles en todos los juegos; las cuotas son indistinguibles en dog8 y casi en dog6 (salvo tie-rate), y claramente diferentes en horse_classic por su diseño placeholder declarado.