# Borrador — Pre-consulta a GLI (RNG evaluation, vg-racegen)

> Uso interno. Enviar vía Client Services / "Ask GLI" antes de la sumisión formal
> (docs/PLAN-CERTIFICACION-GLI19.md, paso 0.1). Rellenar los campos [..] y traducir
> al inglés para el envío. Adjuntar este cuestionario técnico; NO adjuntar código
> todavía (eso va en la sumisión por Point.Click.Submit).

---

**Subject:** Pre-submission technical questionnaire — RNG evaluation (GLI-19 v3.0), virtual racing outcome generator

**Company:** [razón social] · **Contact:** [nombre, cargo, email] · **Product:** vg-racegen — standalone virtual greyhound/horse racing round generator (Go)

## 1. Product summary (for scoping)

- RNG: HMAC-DRBG SHA-256 per NIST SP 800-90A §10.1.2, seeded/reseeded from the OS
  CSPRNG (`crypto/rand`, Linux `getrandom(2)`). 256-bit strength. Implementation
  validated against NIST CAVS 14.3 known-answer vectors.
- Scaling: rejection sampling (exact-uniform integers), 53-bit uniform floats.
- Outcome model: pre-recorded video selection — one RNG draw selects a video (and
  its fixed finishing order) from a weighted catalog fitted by IPF to the declared
  per-runner win/place probabilities. Odds are presentation, generated after the
  outcome, preserving the per-round odds-value multiset (RTP independent of the
  assignment step).
- Lab reproducibility: a `gli_lab` build flavor replaces ONLY the entropy source
  with a deterministic HKDF expander of a 32-byte seed (same DRBG, same triggers,
  same game code). Production binaries verifiably contain no deterministic-seed
  path (symbol-table gate in CI).
- Collection tools per Composite Submission Requirements v2.0 §2.2: Raw Output
  (binary, GB-scale) and Final Outcome (CSV, full production pipeline) — both call
  the production code directly.

## 2. Questions for GLI

1. **Scope boundary.** We are submitting the RNG for certification. Please confirm
   the expected scope: (a) DRBG + scaling primitives only, or (b) including the
   number→symbol mapping (IPF-weighted video selection and the odds-assignment
   permutation). Our tools and documentation cover (b); we want the engagement
   quoted accordingly.
2. **OS CSPRNG as entropy source.** Please confirm that the OS CSPRNG
   (`getrandom(2)`) is acceptable as the live entropy source for instantiate and
   reseed of an SP 800-90A DRBG, and whether you require an SP 800-90B-style
   entropy rationale document for it.
3. **RNG strength & monitoring (GLI-19 v3.0).** Does the operational RNG
   monitoring requirement apply to a server-side interactive system of this type
   as a software obligation, and is a rolling-window statistical monitor
   (chi²/runs with alert + game pause) an acceptable implementation?
4. **Statistical data volumes.** For a 6/8/7-runner virtual racing product, please
   confirm the draw volumes you will require for (a) raw output and (b) scaled
   game outcomes, and the preferred file formats — we will send a small
   preliminary sample for format verification before bulk collection, per your
   technical specifications page.
5. **Reproducibility for the test bench.** Is the `gli_lab` build-flavor approach
   (deterministic entropy expander behind the same DRBG, seed supplied by the
   tester, production build rejects seeds) acceptable as the known-seed replay
   mechanism for your source review and data reproduction?
6. **Jurisdictions.** Target jurisdictions are in Latin America: [lista —
   p. ej. Brasil (SPA/MF), Colombia (Coljuegos), Perú (MINCETUR), provincias de
   Argentina]. Please confirm which jurisdictional checklists would apply on top
   of GLI-19 and whether a single evaluation can be extended per jurisdiction.
7. **Quote.** Please provide a quotation and expected timeline for the above
   scope, including the source-code review modality (at GLI vs. on-site).

## 3. Attachments checklist (when submitting formally)

- [ ] Request letter with target jurisdictions (Point.Click.Submit)
- [ ] Source code (final) + compiled binaries + SHA-256 manifest
- [ ] RNG Description and Documentation (`docs/DESCRIPCION-TECNICA-RNG.md`, EN translation)
- [ ] Technical Source Code Description (flow: instantiation → final outcome)
- [ ] Raw Output Collection Tool + source (`cmd/rngextract -mode bits`)
- [ ] Final Outcome Collection Tool + source (`cmd/rngextract -mode game`)
- [ ] Game parameters & draw rules (ranges, with/without replacement per draw)
- [ ] Preliminary data sample (format check)
- [ ] Statistical results package (NIST SP 800-22, Dieharder, PractRand, chi² per
      symbol/range, autocorrelation between consecutive races) with seeds, tool
      versions and hashes recorded
