# Plan de certificación GLI-19 v3.0 — RNG de vg-racegen (PLAN DEFINITIVO, v2)

**Repo:** `/home/claude/projects/vg-racegen` · **Fecha:** 2026-06-12 · **Estándar objetivo:** GLI-19 v3.0 cap. 3 (+ §4.5.2, §4.17.1 → GLI-33) · **Estado:** v2 tras revisión adversarial de 3 evaluadores (2 × "certificable con plan", 1 × "rechazo probable"). Las 12 objeciones bloqueantes/mayores están integradas en los pasos; las 11 menores, resueltas en pasos y respondidas en §3.

---

## Resumen ejecutivo

vg-racegen NO es certificable hoy: su fuente primaria es MT19937 con semilla determinista obligatoria en producción y registrada en claro en el audit log (H1/H2/H3). Este plan lo remedia sustituyendo la fuente por un HMAC-DRBG SHA-256 (SP 800-90A) sembrado de `crypto/rand`, con modo laboratorio por build tag que fija únicamente la entropía de entrada (misma implementación en ambos builds, vía interfaz `EntropySource`). La revisión adversarial añadió dos hallazgos que ahora son de primera clase: (1) el pairing vídeo↔resultado de dog8/dog6 procede de JSON de vendor sin verificar contra el contenido real de los vídeos — el propio `embed.go:44-45` admite el desalineamiento — y se resuelve con verificación de contenido del catálogo completo más manifiesto de checksums (paso 3.6, bloqueante de la sumisión); y (2) el warm-restart regenera rondas ya persistidas consumiendo RNG y escribiendo entradas de audit divergentes de la DB — y sobrescribe rondas pendientes dejando GameRounds y GameResults incoherentes — lo que se corrige haciendo el boot consulta-primero (paso 2.7). El modo `gli_lab` queda especificado de forma reproducible (expansión HKDF de la semilla para instantiate y cada reseed, disparadores de reseed deterministas, personalization fija). La Final Outcome Collection Tool produce resultados de juego completos (`-mode game`), no solo uniformes escaladas. El alcance GLI-33 deja de ser un placeholder: gap analysis requisito-por-requisito del lado productor como entregable de Fase 3. La decisión del jackpot (C4) pasa a ser bloqueante de la Fase 2 para garantizar un único rebaseline. Se añade cronograma con esfuerzo, ruta crítica y asignación nominal — sumisión objetivo: última semana de octubre de 2026; certificado estimado: Q1 2027. Regla de oro intacta: ninguna evidencia estadística ni documental se genera sobre el MT19937 saliente.

---

## Disposición de las objeciones (trazabilidad de esta revisión)

Las 23 objeciones fueron contrastadas contra el código. **Ninguna se refuta en su núcleo; todas se aceptan e integran.** Dos matizaciones verificadas:

- **Warm-restart (E3, mayor) — confirmada y AMPLIADA:** el comentario de `main.go:257-259` ("on a warm restart the slots already in the DB are upsert-skipped") es falso en lo que importa: `generateAndPersist` (main.go:610-670) consume el stream y emite `game_generated` ANTES de que `UpsertGameRound` decida saltar. Peor: para rondas pendientes (Status≠'F') con odds distintas — inevitable con entropía del SO — el upsert SOBRESCRIBE GameRounds (VideoName/odds nuevos) mientras `stmtInsertResult` es `INSERT OR IGNORE` (sqlite.go:350) y conserva los GameResults viejos → divergencia interna GameRounds↔GameResults en cada deploy, no solo audit↔DB. El paso 2.7 cubre ambas.
- **Pairing de vídeo (E3, bloqueante) — confirmada literalmente:** `embed.go:16-32` documenta que los pools dog8 (411) y dog6 (979) vienen de JSON de virteon-platform sin verificación de contenido, y `embed.go:43-45` declara que solo horse_classic fue reconstruido de los MP4 reales y que el pairing de relay.db "is misaligned — same as dogs". Paso 3.6 nuevo.

| Objeción (severidad) | Resolución |
|---|---|
| Pairing vídeo↔resultado dog8/dog6 (bloqueante) | Paso **3.6** nuevo + ítem 16 del paquete + gate de sumisión |
| Sin cronograma/esfuerzo/equipo (bloqueante) | Paso **0.4** nuevo + columna de esfuerzo + §1.bis calendario y ruta crítica |
| Replay gli_lab contradictorio (mayor ×2) | Pasos **2.1/2.2 reescritos**: `EntropySource` + HKDF + triggers deterministas + semántica de GenerationCount |
| Final Outcome Tool sin outcomes de juego (mayor) | Paso **5.1**: `-mode game` como ítem 2 del paquete |
| Reclasificación unilateral §3.3.3/R12 (mayor) | Pasos **0.1 y 7.1**: pregunta explícita en pre-consulta; R14 presenta el monitor como cumplimiento de R12 sin condicionarlo |
| GLI-33 sin gap analysis (mayor) | Paso **3.7** nuevo, entregable F0-F3 |
| Sin verificación del software en campo (mayor) | Paso **4.4** nuevo |
| Warm-restart audit↔DB (mayor) | Paso **2.7** nuevo |
| Grafo F4/F5 erróneo (mayor) | Grafo corregido: F3.3/3.4 → F4.1, F5.4 |
| Jackpot decidido tarde rompe rebaseline único (mayor) | **C4 movida a Fase 0**, gate de entrada de Fase 2 |
| Commit-reveal sin canal de publicación (mayor) | Paso **4.2 reescrito** con dueño, canal y criterio |
| 11 menores (CRNGT, entropía 90B, nm/strip, RTP, estabilidad numérica, commit-reveal≠no-fuga, matriz trazabilidad, gestión de cambios, skill, dto_full, KMS) | Integradas en pasos 0.2, 0.3, 2.1, 2.2, 2.5, 3.1, 3.4, 4.2, 4.3, 5.3, 6.2, 7.1, 7.6 + respuestas en §3 |

---

## 0. Decisiones de arquitectura (revisadas)

| # | Cuestión | Decisión | Cambio vs v1 |
|---|---|---|---|
| D1 | Fuente | **HMAC-DRBG SHA-256 (SP 800-90A §10.1.2)** sembrado de crypto/rand; crypto/rand directo como alternativa pre-acordada | Sin cambio |
| D1.b *(nueva)* | Abstracción de entropía | **Interfaz `EntropySource`** (`Instantiate() [48]byte; Reseed(counter uint64) [32]byte`): prod = crypto/rand; lab = expansor determinista HKDF-SHA256 de la semilla de 32 B. **El código del DRBG y de los triggers es idéntico en ambos builds**; solo la implementación de EntropySource cambia por tag. Esto hace verdadera la afirmación "same RNG and methods" del ítem 2 | Resuelve la contradicción del replay (2 evaluadores) |
| D1.c *(nueva)* | Disparadores de reseed | **Solo deterministas**: cada 10.000 generates Y en cada frontera de ronda. **Se elimina el trigger horario** (dependía del reloj de pared y hacía el replay inalcanzable); la cota temporal queda cubierta de facto porque una ronda dura 240 s → reseed inter-ronda ≪ 1 h siempre | Cambio sobre 2.1 v1 |
| D2 | Monitoreo R12 | Se implementa igual; **la clasificación hardware/software de §3.3.3 se PREGUNTA en pre-consulta (0.1) y NO se fija en el expediente**: R14 presenta el monitor como cumplimiento de R12, válido bajo cualquiera de las dos lecturas | Reformulada |
| D3 | CertifiedFloat 53 bits | Sin cambio; **rebaseline único garantizado moviendo C4 (jackpot) a Fase 0** | Reforzada |
| D4 | NormalClamped por inversa de CDF truncada | Sin cambio, **con rama de cola simétrica vía erfc** (evita cancelación catastrófica cuando [min,max] cae en una cola) y cota de error documentada por cada juego de parámetros de producción | Ampliada |
| D5 | H3 crítica; el log registra solo `SHA-256(seed material)` | Sin cambio |
| D6 | MT19937 solo bajo `gli_lab`; **evidencia de ausencia vía twin build sin strip** (ver 2.2/6.2) | Mecanismo de verificación corregido |
| D7 | GLI primario, cotización paralela BMM/iTech | Sin cambio |
| D8 | Skill `gli-rng-certification` prohibida; **su retirada es un ítem de gestión de configuración del entorno con dueño = cliente (NEG)**, no una acción sobre el repo | Reformulada (a fecha de hoy la skill sigue instalada y activa en el entorno de tooling) |
| D9 *(nueva)* | Catálogo de vídeo | El pairing vídeo↔resultado es **parte del artefacto certificado**: verificación de contenido al 100% + manifiesto SHA-256 de assets + pinning de versión. horse_classic ya cumple por construcción (filename = orden de llegada, embed.go:34-45); dog8/dog6 deben verificarse o reconstruirse igual | Nueva |
| D10 *(nueva)* | Gestión de secretos | La clave HMAC del audit (3.1) y cualquier custodia de semilla lab van a **docker secrets / KMS externo aprovisionado en compose** — hoy no existe en el stack; se añade paso de aprovisionamiento (3.1) | Nueva |

---

## 1. Plan paso a paso hacia la certificación

**Roles (asignación nominal, paso 0.4):** ARQ = arquitecto/cripto · GO = senior Go (hoy: marcos-ugarte, único committer — **la Fase 5/QA requiere una segunda persona o contratación**, ver C9) · QA = estadístico · OPS = DevOps · DOC = compliance · NEG = cliente (jorge@disercoin.com).

### Fase 0 — Pre-consulta, decisiones y saneamiento *(esfuerzo ≈ 8 días-persona · semanas 1-2)*

| Paso | Qué se hace | Quién | Criterio de salida |
|---|---|---|---|
| 0.1 | Pre-consulta GLI (+ BMM/iTech): arquitectura objetivo y **lista cerrada de cuestiones a confirmar por escrito**: (a) HMAC-DRBG 90A como "recognized cryptographic algorithm"; (b) **clasificación de §3.3.3/R12 para RNG software** — se pregunta, no se presume; nuestra implementación cumple bajo cualquier lectura; (c) alcance GLI-33 por §4.17.1 y reparto productor/plataforma; (d) aceptabilidad de getrandom(2) como fuente de entropía del DRBG (rationale 90B, ver 7.1); (e) volúmenes de draws y formato; (f) ventanas de calendario contratables | NEG + ARQ | Acta escrita con las 6 respuestas (u objeciones tempranas); cotizaciones |
| 0.2 | Purga documental: corregir comentarios invertidos (`mt19937.go:1-5,77`, `main.go:14-15,786-794` — el bloque que justifica la semilla obligatoria citando "GLI-19 §3.3"); **retirada de la skill `gli-rng-certification` como ítem de gestión de configuración del entorno de desarrollo, dueño NEG, evidencia = inventario de skills del entorno firmado y fechado** (la skill no vive en el repo; el grep solo cubre el código) | DOC + GO + NEG | `grep -ri "GLI" internal/ cmd/` sin citas que justifiquen decisiones contrarias al estándar; inventario firmado sin la skill |
| 0.3 | Higiene de repo CON revisión de seguridad: `mon` y `cmd/mon` fuera del árbol o a su sitio; **`internal/feed/dto_full.go` NO se commitea como limpieza: pasa primero por el test de no-fuga de 4.3** (es una superficie de revelado nueva con boundary distinto — VideoStartDt — del slim — VideoEndDt; dos boundaries en el mismo feed son pregunta segura del laboratorio y deben documentarse juntos) | GO | `git status` limpio; dto_full con test de no-fuga en betting verde y doc de los dos boundaries |
| 0.4 | **Plan de ejecución contratable**: estimaciones por paso (esta tabla), ruta crítica con fechas (§1.bis), asignación nominal de personas a roles, decisión de refuerzo de equipo (C9), presupuesto de cómputo (C7) y de laboratorio aprobados ANTES de comprometer ventanas en 0.1/7.3 | NEG + ARQ | Calendario aprobado y firmado por el cliente; presupuesto autorizado |
| 0.5 | **Decisión C4 (jackpot) — AQUÍ, no en Fase 7**: el jackpot consume 2-3 draws por ronda (`game.go:213-220`: increment + reset condicional + histAmount); retirarlo después del rebaseline 2.6 invalidaría goldens y ≥30 M de rondas simuladas. Gate: **Fase 2 no arranca sin C4 decidida por escrito** | NEG | Decisión escrita; si se retira, el cambio entra junto con 2.5/2.6 en el mismo rebaseline |

### Fase 1 — Refactor de interfaz *(≈ 7 días-persona · semana 3)*

Sin cambios sobre v1 (pasos 1.1 interfaz `Source` con `GenerationCount()`, 1.2 stub tests de ramas intesteables, 1.3 guards de totalidad). Nota añadida a 1.1: la semántica de `GenerationCount` se define ya aquí como **contador monótono de llamadas a generate, jamás reseteado por reseed** (ver 2.1.c) para que `mtSeqAfter` del audit sea estable a través de la migración.

### Fase 2 — Fuente criptográfica + seeding + restart *(≈ 22 días-persona · semanas 4-7; gate de entrada: C1 y C4 decididas)*

| Paso | Qué se hace | Quién | Criterio de salida |
|---|---|---|---|
| 2.1 | `internal/racegen/rng/hmacdrbg.go` (SP 800-90A §10.1.2, security strength 256) sobre **`EntropySource`** (D1.b). Especificación completa del modo determinista — lo que el evaluador pidió "conectar": **(a)** instantiate consume `EntropySource.Instantiate()` = 48 B (32 entropy + 16 nonce); **(b)** CADA reseed consume `EntropySource.Reseed(counter)` = 32 B; en prod ambos leen crypto/rand; en lab ambos derivan por **HKDF-SHA256(master=RACEGEN_SEED_HEX 32 B, info="instantiate" \| "reseed:"+counter)** — la validación actual de 64 hex (`main.go:778-784`) se conserva: 32 B de master seed expanden a todo lo necesario; **(c)** personalization string = **constante `"vg-racegen|"+gameType`** (se eliminan boot-ts y hostname: la unicidad de instanciación la da la entropía, y en lab deben ser deterministas); **(d)** triggers de reseed solo deterministas (D1.c): cada 10.000 generates y en frontera de ronda — sustituye y elimina el background cycling H4; **(e)** `GenerationCount` = generates acumulados, monótono a través de reseeds; cada reseed se audita con su índice de generación → el replay y `mtSeqAfter` son comprobables; **(f)** prediction_resistance=false documentado. Salud: self-test CAVP al boot fail-closed; el continuous test FIPS se conserva pero **clasificado como defensa-en-profundidad, no como evidencia** (FIPS 140-3 retiró el CRNGT para DRBGs); el control con valor probatorio es el rationale de la fuente de entropía (7.1) y el fail-closed del canal de reseed | ARQ + GO | KATs CAVP en CI verdes; **test de replay: dos corridas lab con la misma semilla y distinto reloj de pared producen streams bit-idénticos, incluidas las fronteras de reseed**; reseeds auditados con generation index |
| 2.2 | Inversión de seeding: `source_prod.go` (`!gli_lab`) siembra del SO y **rechaza fail-closed RACEGEN_SEED_HEX** (inversión exacta de `main.go:786-794`); `source_lab.go` (`gli_lab`) instala el EntropySource HKDF. MT19937+state_modifier migran a archivos `gli_lab` o se eliminan; fuera del binario de producción `State()/RestoreState()` y `StateBefore/StateAfter`. **Verificación de ausencia corregida** (2 evaluadores): el release lleva `-s -w`, así que la evidencia es un **twin build del mismo commit con flags idénticos salvo strip** (reproducible → mismo código máquina); `go tool nm` se ejecuta sobre el twin; el laboratorio recibe el procedimiento de rebuild witnessed que liga twin↔release. CI compila y testea ambos tags | GO | Ambas variantes verdes en CI; twin build sin símbolos MT ni camino seed-hex; prod con RACEGEN_SEED_HEX presente falla con error explícito |
| 2.3 | Audit log sin semilla: `init` registra `SHA-256(seed material)` + identificador de fuente; reseeds como eventos sin material; custodia de semilla lab en secreto gestionado (D10), nunca en JSONL | GO | Test: cero material de semilla en el JSONL |
| 2.4 | Fail-safe real: sentinel `rng.FatalError` desde instantiate/reseed/self-test fallidos; **modificar el recover de `tickRunner` (main.go:439-448)** para terminar el proceso ante `rng.FatalError` (hoy tragaría el pánico y seguiría generando) | GO | Test de integración: fallo inyectado → proceso termina sin avanzar slot ni emitir ronda |
| 2.5 | Correcciones matemáticas: CertifiedFloat a 53 bits; NormalClamped por inversa de CDF truncada **con rama simétrica/erfc** (trabajar siempre en la cola con Φ̄ = erfc(x/√2)/2 — sin esta rama, configs con [min,max] en cola sufren cancelación y la resolución efectiva cae muy por debajo de los 53 bits reivindicados) y **cota de error documentada por cada juego de parámetros de config en producción** (entra en R14, 7.1); consumo fijo de 1 uniforme/draw; documentar o corregir el sesgo de borde de FloatRange | GO + ARQ | KS/chi² contra distribuciones teóricas; tabla de cotas de error por config; consumo de stream determinista verificado |
| 2.6 | Rebaseline ÚNICO de goldens (fuente nueva + 53 bits + NormalClamped + decisión C4 ya aplicada); fix de reintento de slot sin doble consumo | GO | Suite verde en ambos tags; un solo rebaseline en todo el plan, anotado |
| 2.7 | **Warm-restart correcto** (nuevo; mayor confirmado y ampliado): `bootBackfill`/`bootBulk` (main.go:261-264, 330-374) **consultan la DB ANTES de generar y saltan los slots ya persistidos sin consumir RNG ni emitir audit** (hoy `generateAndPersist` genera y audita SIEMPRE y el descarte ocurre después en `UpsertGameRound`, sqlite.go:495-531); corregir además la sobrescritura de rondas pendientes: una ronda ya persistida y no finalizada NO se regenera (hoy el upsert pisa GameRounds con odds/vídeo nuevos mientras `INSERT OR IGNORE` conserva los GameResults viejos → incoherencia interna). Es prerequisito del commit-reveal (4.2): los commitments publicados deben sobrevivir al restart con la misma preimagen | GO | **Test de restart**: matar y rearrancar a mitad de horizonte → cero entradas `game_generated` nuevas para slots existentes, audit↔DB coherentes, GameRounds↔GameResults coherentes, commitments estables |

### Fase 3 — Integridad: audit, IPF, catálogo de vídeo, GLI-33 *(≈ 25 días-persona + QA de visionado · semanas 7-11)*

Pasos 3.1-3.4 como en v1, con estos cambios:

| Paso | Cambio / contenido |
|---|---|
| 3.1 | Endurecimiento del audit (HMAC con clave gestionada, fsync, fix resumeChain, verificación periódica) **+ paso de aprovisionamiento de la clave** (D10): docker secrets en compose o KMS externo, con runbook de rotación — hoy ese servicio no existe en el stack y la "clave gestionada" era papel mojado |
| 3.2 | Verificación post-fit del IPF con residuo L1 y fallo de arranque — sin cambios |
| 3.3 | Marginales post-IPF como artefacto JSON versionado — sin cambios; **es insumo bloqueante de 4.1 y 5.4** (grafo corregido) |
| 3.4 | Paquete probatorio cuotas↔resultado. **Fórmulas de RTP corregidas con convención de stake explícita** (objeción matemática aceptada): para la política "uniforme por índice" con stake unitario, **RTP = (1/n)·Σ pᵢ·oddsᵢ**; la identidad RTP = E[cuota del ganador] solo vale para la política "apostar a los n resultados" con stake total n. La hoja PAR declara la convención de cada política; la simulación ≥10 M rondas/gameType con IC 99% se mantiene |
| 3.5 | Implementación de la decisión C4 (ya tomada en 0.5) — aquí solo se ejecuta y documenta |
| **3.6** *(nuevo — resuelve la objeción BLOQUEANTE)* | **Verificación de contenido del catálogo de vídeos dog8/dog6**: (a) QA del frame de llegada de las 411 entradas dog8 + 979 dog6 contra el `Order` embebido — OCR/visionado semiautomatizado con doble verificación humana de discrepancias, evidencia archivada (screenshot del frame + registro firmado por entrada); (b) si aparecen desalineamientos, **reconstruir el pool desde los vídeos reales como ya se hizo con horse_classic** (embed.go:34-45 es la plantilla del método correcto); (c) **manifiesto SHA-256 de cada MP4/JPG** del catálogo (los assets viven fuera del repo bajo `cfg.VideoPoolPath`; `finish.go:177-193` solo construye la URL) + pinning de versión del catálogo en el build; (d) verificación de completitud: existe vídeo para TODO orden de llegada con probabilidad >0 post-IPF (complementa 3.2: aquello es probabilidad, esto es presentación); (e) custodia del catálogo en el paquete de sumisión (ítem 16) y verificación del manifiesto al arranque del servidor de assets o, si el front está fuera de alcance, frontera documentada + manifiesto entregado al operador. **Dueño QA + GO. Gate: la sumisión (7.4) no sale sin 3.6 cerrado al 100% para los tres gameTypes** — sin esto, la pregunta "¿cómo sé que el vídeo mostrado corresponde al resultado liquidado?" no tiene respuesta para 2 de 3 juegos |
| **3.7** *(nuevo)* | **Gap analysis GLI-33 requisito-por-requisito del lado productor** (deja de ser placeholder de pre-consulta): integridad y completitud del catálogo de eventos (←3.6), correspondencia vídeo↔resultado inalterable post-selección, información mostrada no engañosa (←C4/jackpot), retención y replay de eventos, sincronización temporal. Cada requisito → cumple/no aplica (recae en plataforma, con frontera documentada)/acción. Entregable de esta fase; la pre-consulta 0.1.c solo VALIDA el reparto, no lo sustituye | 

### Fase 4 — Controles operacionales *(≈ 12 días-persona · semanas 10-12; 4.1 requiere 3.3)*

| Paso | Cambio / contenido |
|---|---|
| 4.1 | Monitor estadístico en ventana móvil contra las marginales de **3.3 (dependencia explícita — NO arrancar antes ni trabajar contra el target de config)**; presentado en R14 como cumplimiento de R12 sin condicionarlo a la lectura hardware/software (D2); pausa+alerta, nunca autocorrección |
| 4.2 | Custodia de resultados pre-generados, **reordenada por lo que prueba cada control** (objeción aceptada): contra la FUGA (insider lee relay.db y apuesta) → aislamiento de host/red, deny-by-default del feed, logging de acceso, NTP autenticado; contra la SUSTITUCIÓN (alterar el resultado tras abrir apuestas) → commit-reveal. **Canal de publicación del commit-reveal definido con dueño** (objeción mayor): opción (a) endpoint público de commitments operado por vg-racegen (`/v1/commitments`, respaldado por el hash-chain del audit y espejado a un log externo append-only — p. ej. publicación periódica del head-hash a un servicio de timestamping/tercero), u opción (b) acuerdo escrito con la plataforma consumidora que la obliga a exponer commitment y reveal al jugador. Decisión C5 ampliada con este eje; criterio de salida: un tercero SIN acceso a relay.db puede verificar commitment→reveal de una ronda real |
| 4.3 | Test de no-fuga del DTO en estado betting **cubriendo AMBAS superficies: slim (gate VideoEndDt) y full (gate VideoStartDt)** — dto_full entra aquí desde 0.3; documentación conjunta de los dos boundaries y su justificación (`dto_full.go:18-26` ya la esboza: videoName se revela al cierre de apuestas, igual que el /tv) |
| **4.4** *(nuevo — resuelve mayor)* | **Verificación del software desplegado en campo**: self-hash del binario al arranque comparado contra el hash certificado y registrado en el audit log; endpoint/runbook de verificación on-demand (hash del binario en ejecución + del módulo rng dentro del contenedor) para inspección del regulador; procedimiento escrito de verificación en sitio. Cierra la cadena hash certificado → artefacto desplegado que la Fase 6 solo cubría en compilación |

### Fase 5 — Harness y baterías estadísticas *(≈ 15 días-persona + 4-6 semanas de cómputo · semanas 8-16; 5.4 requiere 3.3/3.4)*

| Paso | Cambio / contenido |
|---|---|
| 5.1 | `cmd/rngextract` con TRES modos: `-mode raw` (stream del DRBG pre-escalado) y `-mode scaled` (Certified* reales) como en v1, **más `-mode game` (nuevo — resuelve mayor): genera resultados de juego COMPLETOS (orden de llegada, videoID, cuotas, bonus) importando el pipeline íntegro de producción (DRBG → Certified* → CDF post-IPF → videoselector → generators), salida parseable + sidecar de trazabilidad**. `-mode game` es la **RNG Final Outcome Collection Tool** del ítem 2 (el ejemplo oficial de GLI — 51 M draws para 5-de-49 — son outcomes de juego, no uniformes escaladas) y la herramienta con la que 3.4 y 5.4 simulan sus ≥10 M rondas (deja de estar sin especificar). `-mode scaled` queda como evidencia suplementaria del escalado. Bajo `gli_lab` los tres modos aceptan `-seed-hex` y **reproducen las fronteras de reseed** (triggers deterministas de 2.1.d) |
| 5.2 | Stream A (NIST SP 800-22, Dieharder, PractRand ≥32 GB) con criterios predefinidos y re-ejecuciones registradas — sin cambios |
| 5.3 | Stream B — **se añade la misma política de re-ejecución registrada que 5.2** (con decenas de tests y banda p ∈ [1e-4, 0.9999], un false-fail es esperable ~0.02%/test; el procedimiento predefinido evita improvisar ante el laboratorio) |
| 5.4 | Stream C (nivel juego) usando `-mode game` contra las marginales de 3.3 — dependencia explícita de F3 |

### Fase 6 — Build reproducible *(≈ 6 días-persona · semanas 8-9)*

6.1 sin cambios (pin de toolchain, build canónico con `-trimpath -s -w`, doble build bit-idéntico). 6.2 **incorpora el twin build sin strip** como artefacto estándar del pipeline (mismo commit, mismos flags salvo `-s -w`), publicado junto al release con su SHA-256: es la base de la verificación de símbolos de 2.2 y del rebuild witnessed del laboratorio — se cierra el círculo que v1 dejaba abierto.

### Fase 7 — Documentación, pre-sumisión y entrega *(≈ 18 días-persona · semanas 13-17 + acompañamiento)*

| Paso | Cambio / contenido |
|---|---|
| 7.1 | RNG Description (R14) — se añaden: **anexo de fuente de entropía (rationale SP 800-90B)**: por qué getrandom(2)/CSPRNG del kernel es aceptable como entropy source del DRBG, declaración explícita de la asunción full-entropy al estilo 90C, comportamiento en arranque temprano de contenedor (bloqueo hasta pool inicializado) y en hosts clonados/snapshots (relevante: despliegue Docker Compose), salud del canal de reseed, y nota de que crypto/rand en Go ≥1.24 no retorna error; **tabla de cotas de error de NormalClamped por config** (2.5); **matriz de trazabilidad GLI-19 cap. 3 → evidencia** (cada cláusula → archivo:línea, test, dato o documento; se construye actualizando la tabla R1-R14 de `docs/AUDITORIA-RNG-GLI19.md` al diseño post-remediación) como anexo del ítem 4 |
| 7.2 | Game Description + Technical Source Code Description — sin cambios, añadiendo el catálogo de vídeo verificado (3.6) y el gap analysis GLI-33 (3.7) como anexos |
| 7.3 | Pre-submission review — sin cambios (el calendario que el laboratorio confirma ahora EXISTE: §1.bis) |
| 7.4 | Sumisión — gate: 3.6 cerrado al 100% |
| 7.5 | Acompañamiento — sin cambios |
| **7.6** *(nuevo)* | **Procedimiento de gestión de cambios post-certificación** como documento del paquete (ítem 17): clasificación de cambios — el módulo rng es lo certificado, pero **cambios en consumidores del stream (odds.go, selector.go, game.go) que alteran el mapeo número→símbolo o el consumo por ronda disparan re-evaluación**, no solo notificación; aprobadores, registro de versiones, triggers re-test vs notificación; política de árbol limpio permanente (la situación de hoy — `internal/feed/` sin commitear, binarios sueltos — no debe poder reproducirse tras la certificación sin romper el proceso) |

### §1.bis — Calendario y ruta crítica (semana 1 = 15-jun-2026)

| Semanas | Hito |
|---|---|
| 1-2 | F0 completa: pre-consulta enviada, C1/C3/C4/C9 decididas, calendario aprobado |
| 3 | F1 (interfaz Source) |
| 4-7 | F2 (DRBG + seeding + fail-safe + restart + rebaseline único) |
| 7-11 | F3 (audit, IPF, **catálogo de vídeo 3.6 — el QA de 1.390 vídeos arranca la semana 7 en paralelo**, GLI-33) |
| 8-9 | F6 (build reproducible + twin build) |
| 8-12 | F5.1-5.3 (harness + Streams A/B; cómputo en cloud) |
| 10-12 | F4 (monitor tras 3.3, commit-reveal, no-fuga, verificación en campo) |
| 12-16 | F5.4 + 3.4 (simulación ≥30 M rondas, RTP) |
| 13-17 | F7.1-7.3 (documentación + pre-submission review) |
| **18 (≈ 26-oct-2026)** | **7.4 Sumisión** |
| +8 a +16 semanas | Revisión del laboratorio + RFIs → **certificado estimado Q1 2027** |

**Ruta crítica:** F0 → F1 → F2 → F3(3.3/3.4/3.6) → F5.4 → F7. Holguras: F4 (2 sem), F6 (4 sem). **Riesgo de capacidad:** con un solo desarrollador la ruta crítica se alarga ~40%; la decisión C9 (refuerzo QA/visionado + estadístico) debe tomarse en la semana 1.

```
F0 (pre-consulta + decisiones C1/C3/C4/C9 + saneamiento)
 └→ F1 → F2 (incl. 2.7 restart) ──┬→ F3.1-3.7 ──┬→ F4.1 (necesita 3.3)
                                  │             ├→ F5.4 (necesita 3.3 + 3.4 + 5.1)
                                  ├→ F4.2/4.3/4.4        [paralelo]
                                  ├→ F5.1-5.3             [paralelo]
                                  └→ F6                   [paralelo]
F7 ← F3, F4, F5, F6   ·   Gate de 7.4: 3.6 al 100%
```

---

## 2. Paquete de sumisión al laboratorio (checklist actualizada)

Ítems 1-14 de v1 se mantienen con estas correcciones y altas:

| # | Ítem | Cambio |
|---|---|---|
| 2 | RNG Final Outcome Collection Tool | **= `rngextract -mode game`** (outcomes de juego completos por el pipeline de producción); `-mode scaled` pasa a evidencia suplementaria del escalado (junto al ítem 11) |
| 8 | Checksums/firmas | Añade el **twin build sin strip** + procedimiento que lo liga al release |
| 14 | Alcance GLI-33 | Sustituido por el **gap analysis 3.7** (entregable propio, no resultado de pre-consulta) |
| **15** | **Matriz de trazabilidad GLI-19 cap. 3 → evidencia** (anexo del ítem 4, paso 7.1) | Nuevo |
| **16** | **Catálogo de vídeo verificado**: manifiesto SHA-256 de los 1.728 assets (411+979+338), evidencia de QA de contenido por entrada, pinning de versión, procedimiento de custodia | Nuevo — sin él, 2 de 3 gameTypes no tienen defensa GLI-33 |
| **17** | **Procedimiento de gestión de cambios post-certificación** (paso 7.6) | Nuevo |
| **18** | **Evidencia de verificación en campo** (paso 4.4): self-hash al boot, endpoint on-demand, runbook de inspección | Nuevo |

---

## 3. Riesgos residuales y respuestas preparadas

R1-R12 de v1 siguen vigentes con tres reescrituras (R2, R6, R9) y seis altas que responden a las objeciones menores:

| # | Objeción posible del laboratorio | Respuesta preparada |
|---|---|---|
| R2 *(reescrita)* | "Los resultados existen ~100 min antes: un insider puede conocerlos" | Delimitando qué prueba cada control: contra la **fuga** (conocer el resultado): aislamiento de host/red de relay.db, deny-by-default del feed con test de no-fuga en betting sobre ambas superficies DTO, logging de acceso al volumen, NTP autenticado. Contra la **sustitución** (alterar el resultado tras abrir apuestas): commit-reveal con canal de publicación verificable por terceros (4.2). No presentamos el commit-reveal como mitigación de la fuga — son riesgos distintos con controles distintos, y así está documentado. |
| R6 *(reescrita)* | "Existe un modo determinista: ¿cómo sé que no está en producción?" | El modo lab fija solo la ENTROPÍA (EntropySource HKDF) del mismo algoritmo — código del DRBG y triggers idénticos en ambos builds, verificable en el fuente. Evidencia de ausencia en producción: twin build del mismo commit sin strip (`go tool nm` sin símbolos MT ni camino seed-hex), ligado al release por build reproducible; además prod rechaza fail-closed RACEGEN_SEED_HEX. |
| R9 *(reescrita)* | "¿El monitoreo de §3.3.3 aplica a software?" | No lo presumimos: lo preguntamos en pre-consulta (0.1.b) y el monitor 4.1 se presenta como cumplimiento de R12 válido bajo cualquier lectura — si §3.3.3 aplica, lo cumple; si no, es control compensatorio de la pre-generación. Cubierto en ambos sentidos sin fijar interpretación en el expediente. |
| R13 *(nueva)* | "¿Qué fallo detectaría su continuous test FIPS sobre el DRBG?" | Ninguno realista — por eso está clasificado como defensa-en-profundidad, no como evidencia (FIPS 140-3 retiró el CRNGT para DRBGs: la actualización K/V de HMAC-DRBG impide estados atascados). Los controles con valor probatorio están donde ocurren los fallos reales: rationale documentado de la fuente de entropía (anexo 90B en R14), fail-closed de instantiate/reseed y self-test CAVP al boot. |
| R14 *(nueva)* | "¿Resolución efectiva de NormalClamped en colas?" | Implementación con rama simétrica vía erfc (sin cancelación catastrófica cuando [min,max] cae en una cola); cota de error documentada para CADA juego de parámetros de producción en la RNG Description, además de KS/AD empíricos. La objeción que se hizo a los 32 bits (H6) no se reabre con la solución. |
| R15 *(nueva)* | "Su fórmula de RTP en la hoja PAR" | Convención de stake explícita por política: stake unitario uniforme → RTP = (1/n)·Σ pᵢ·oddsᵢ; cubrir los n resultados con stake total n → RTP = E[cuota ganador]/n. Ambas declaradas y validadas por simulación con IC 99%. |
| R16 *(nueva)* | "Dos boundaries de revelado distintos en el mismo feed (VideoStartDt vs VideoEndDt)" | Intencional y documentado: la superficie full revela en betting-close (VideoStartDt), el mismo instante en que el canal /tv revela el vídeo — no añade información; la slim conserva VideoEndDt para /live. Test de no-fuga en CI cubre ambas en estado betting. |
| R17 *(nueva)* | "¿Y si una corrida de Stream B falla por azar?" | Política de re-ejecución predefinida e idéntica a Stream A: todo re-test se registra en el audit chain con offset nuevo; con bandas p ∈ [1e-4, 0.9999] la masa excluida (~0.02%/test) hace esperable algún false-fail — el procedimiento existe antes de necesitarlo. |
| R18 *(nueva)* | "¿Quién custodia la clave HMAC del audit / los secretos?" | Aprovisionamiento real en el stack (docker secrets/KMS, paso 3.1) con runbook de rotación y matriz de acceso — no una 'clave gestionada' nominal. |
| R19 *(nueva)* | "La guía interna incorrecta sobre GLI, ¿sigue en su entorno?" | Retirada como ítem de gestión de configuración del entorno de desarrollo con dueño y evidencia fechada (inventario de skills firmado, 0.2); ninguna decisión del diseño final la cita; los comentarios de código que la reflejaban están corregidos y el diff es parte del expediente. |

---

## 4. Decisiones que debe tomar el cliente

C1-C8 de v1 se mantienen con dos cambios de calendario y dos altas:

| # | Decisión | Cambio |
|---|---|---|
| C4 | Jackpot decorativo | **Plazo adelantado: Fase 0 (paso 0.5), gate de entrada de Fase 2.** Decidir después rompe el rebaseline único e invalida ≥30 M de rondas simuladas. Recomendación intacta: retirarlo o convertirlo en real. |
| C5 | Custodia de pre-generados | Ampliada con el **canal de publicación del commit-reveal**: (a) endpoint público operado por vg-racegen con anclaje externo, o (b) acuerdo contractual con la plataforma consumidora. Recomendación: (a) — no depende de la buena conducta del mismo operador del que se desconfía. |
| **C9** *(nueva)* | **Capacidad de equipo**: el repo tiene un único committer y el plan requiere ~110 días-persona en 18 semanas más QA de visionado de 1.390 vídeos | Contratar/asignar como mínimo: 1 QA para 3.6 y Fase 5 (semanas 7-16) y soporte estadístico para 3.4/5.4; sin refuerzo, mover la sumisión a enero 2027. Decidir en semana 1 (bloquea el calendario de 0.4). |
| **C10** *(nueva)* | **Estrategia ante desalineamientos en 3.6**: si el QA de contenido encuentra pairings erróneos en dog8/dog6 | Recomendación: reconstruir el pool desde los vídeos reales (método ya probado con horse_classic, embed.go:34-45) en lugar de parchear entradas — produce un artefacto defendible por construcción. Presupuestar 1-2 semanas adicionales si ocurre. |

---

**Regla de oro (intacta):** ninguna batería estadística ni expediente sobre el MT19937 actual — toda evidencia se genera tras cerrar las Fases 1-2 (y la decisión C4), porque la certificación queda ligada a los SHA-256 del código sometido (§2.2.2.a).

Referencias internas verificadas en esta revisión: `internal/racegen/data/embed.go:9-54` (procedencia y desalineamiento de pools), `cmd/race-generator/main.go:255-374` (boot), `main.go:610-670` (generateAndPersist: audit/RNG antes del skip), `main.go:771-795` (validación 64-hex y fail-closed invertido), `internal/sqlite/sqlite.go:344-352,495-531` (INSERT OR IGNORE y sobrescritura de pendientes), `internal/racegen/generators/game.go:200-240` (draws del jackpot e historial ficticio), `internal/feed/dto_full.go` (boundary VideoStartDt). Fuentes normativas: las de v1 (GLI-19 v3.0, GLI-11 v3.0, GLI Composite Submission Requirements v2.0 §2.2, BMM Certification Scheme v2.5, NIST SP 800-90A/90B/22, FIPS 140-3 IG sobre CRNGT).