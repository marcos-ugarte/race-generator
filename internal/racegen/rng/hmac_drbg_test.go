package rng

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
)

// scriptedEntropy es una EntropySource de test que sirve chunks predefinidos
// en orden. Falla si se agotan o si el tamaño pedido no coincide — así cada
// KAT también verifica CUÁNTA entropía consume el DRBG y cuándo.
type scriptedEntropy struct {
	chunks [][]byte
}

func (s *scriptedEntropy) Entropy(p []byte) error {
	if len(s.chunks) == 0 {
		return errors.New("scriptedEntropy: agotada")
	}
	c := s.chunks[0]
	if len(c) != len(p) {
		return fmt.Errorf("scriptedEntropy: pedido %d bytes, chunk de %d", len(p), len(c))
	}
	copy(p, c)
	s.chunks = s.chunks[1:]
	return nil
}

func (s *scriptedEntropy) Describe() string { return "test:scripted" }

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex inválido: %v", err)
	}
	return b
}

// TestHMACDRBG_CAVP_NoReseed valida la implementación contra vectores
// oficiales NIST CAVS 14.3 (HMAC_DRBG SHA-256, no reseed), tomados de la
// suite de mbedTLS v3.6.2 (tests/suites/test_suite_hmac_drbg.no_reseed.data),
// que embarca los vectores del CAVP.
//
// Flujo CAVP no_reseed: Instantiate(entropy||nonce, personalization) →
// Generate(1024 bits) descartado → Generate(1024 bits) == expected.
func TestHMACDRBG_CAVP_NoReseed(t *testing.T) {
	cases := []struct {
		name            string
		entropyAndNonce string // 48 bytes: 32 entropy_input + 16 nonce
		personalization string
		expected        string // 128 bytes (2º generate)
	}{
		{
			name:            "SHA-256,256+128,0,0 #0",
			entropyAndNonce: "ca851911349384bffe89de1cbdc46e6831e44d34a4fb935ee285dd14b71a7488659ba96c601dc69fc902940805ec0ca8",
			personalization: "",
			expected:        "e528e9abf2dece54d47c7e75e5fe302149f817ea9fb4bee6f4199697d04d5b89d54fbb978a15b5c443c9ec21036d2460b6f73ebad0dc2aba6e624abf07745bc107694bb7547bb0995f70de25d6b29e2d3011bb19d27676c07162c8b5ccde0668961df86803482cb37ed6d5c0bb8d50cf1f50d476aa0458bdaba806f48be9dcb8",
		},
		{
			name:            "SHA-256,256+128,0,0 #1",
			entropyAndNonce: "79737479ba4e7642a221fcfd1b820b134e9e3540a35bb48ffae29c20f5418ea33593259c092bef4129bc2c6c9e19f343",
			personalization: "",
			expected:        "cf5ad5984f9e43917aa9087380dac46e410ddc8a7731859c84e9d0f31bd43655b924159413e2293b17610f211e09f770f172b8fb693a35b85d3b9e5e63b1dc252ac0e115002e9bedfb4b5b6fd43f33b8e0eafb2d072e1a6fee1f159df9b51e6c8da737e60d5032dd30544ec51558c6f080bdbdab1de8a939e961e06b5f1aca37",
		},
		{
			name:            "SHA-256,256+128,256,0 #0 (con personalization)",
			entropyAndNonce: "5cacc68165a2e2ee20812f35ec73a79dbf30fd475476ac0c44fc6174cdac2b556f885496c1e63af620becd9e71ecb824",
			personalization: "e72dd8590d4ed5295515c35ed6199e9d211b8f069b3058caa6670b96ef1208d0",
			expected:        "f1012cf543f94533df27fedfbf58e5b79a3dc517a9c402bdbfc9a0c0f721f9d53faf4aafdc4b8f7a1b580fcaa52338d4bd95f58966a243cdcd3f446ed4bc546d9f607b190dd69954450d16cd0e2d6437067d8b44d19a6af7a7cfa8794e5fbd728e8fb2f2e8db5dd4ff1aa275f35886098e80ff844886060da8b1e7137846b23b",
		},
		{
			name:            "SHA-256,256+128,256,0 #1 (con personalization)",
			entropyAndNonce: "8df013b4d103523073917ddf6a869793059e9943fc8654549e7ab22f7c29f122da2625af2ddd4abcce3cf4fa4659d84e",
			personalization: "b571e66d7c338bc07b76ad3757bb2f9452bf7e07437ae8581ce7bc7c3ac651a9",
			expected:        "b91cba4cc84fa25df8610b81b641402768a2097234932e37d590b1154cbd23f97452e310e291c45146147f0da2d81761fe90fba64f94419c0f662b28c1ed94da487bb7e73eec798fbcf981b791d1be4f177a8907aa3c401643a5b62b87b89d66b3a60e40d4a8e4e9d82af6d2700e6f535cdb51f75c321729103741030ccc3a56",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			es := &scriptedEntropy{chunks: [][]byte{mustHex(t, tc.entropyAndNonce)}}
			var pers []byte
			if tc.personalization != "" {
				pers = mustHex(t, tc.personalization)
			}
			d, err := NewHMACDRBG(es, pers)
			if err != nil {
				t.Fatalf("instantiate: %v", err)
			}
			out := make([]byte, 128)
			if err := d.generateInto(out); err != nil { // 1º generate: descartado
				t.Fatalf("generate 1: %v", err)
			}
			if err := d.generateInto(out); err != nil { // 2º generate: comparado
				t.Fatalf("generate 2: %v", err)
			}
			if want := mustHex(t, tc.expected); !bytes.Equal(out, want) {
				t.Fatalf("KAT mismatch\n got %x\nwant %x", out, want)
			}
		})
	}
}

// TestHMACDRBG_CAVP_Reseed valida la ruta Reseed contra vectores NIST CAVS
// 14.3 (HMAC_DRBG SHA-256, PR False), de mbedTLS v3.6.2
// (tests/suites/test_suite_hmac_drbg.nopr.data).
//
// Flujo CAVP pr_false: Instantiate(48B) → Reseed(32B) → Generate descartado
// → Generate == expected. El campo del vector concatena los 80 bytes.
func TestHMACDRBG_CAVP_Reseed(t *testing.T) {
	cases := []struct {
		name     string
		material string // 80 bytes: 48 instantiate + 32 reseed
		expected string
	}{
		{
			name:     "SHA-256,0,0 #0",
			material: "06032cd5eed33f39265f49ecb142c511da9aff2af71203bffaf34a9ca5bd9c0d0e66f71edc43e42a45ad3c6fc6cdc4df01920a4e669ed3a85ae8a33b35a74ad7fb2a6bb4cf395ce00334a9c9a5a5d552",
			expected: "76fc79fe9b50beccc991a11b5635783a83536add03c157fb30645e611c2898bb2b1bc215000209208cd506cb28da2a51bdb03826aaf2bd2335d576d519160842e7158ad0949d1a9ec3e66ea1b1a064b005de914eac2e9d4f2d72a8616a80225422918250ff66a41bd2f864a6a38cc5b6499dc43f7f2bd09e1e0f8f5885935124",
		},
		{
			name:     "SHA-256,0,0 #1",
			material: "aadcf337788bb8ac01976640726bc51635d417777fe6939eded9ccc8a378c76a9ccc9d80c89ac55a8cfe0f99942f5a4d03a57792547e0c98ea1776e4ba80c007346296a56a270a35fd9ea2845c7e81e2",
			expected: "17d09f40a43771f4a2f0db327df637dea972bfff30c98ebc8842dc7a9e3d681c61902f71bffaf5093607fbfba9674a70d048e562ee88f027f630a78522ec6f706bb44ae130e05c8d7eac668bf6980d99b4c0242946452399cb032cc6f9fd96284709bd2fa565b9eb9f2004be6c9ea9ff9128c3f93b60dc30c5fc8587a10de68c",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			material := mustHex(t, tc.material)
			es := &scriptedEntropy{chunks: [][]byte{material[:48], material[48:80]}}
			d, err := NewHMACDRBG(es, nil)
			if err != nil {
				t.Fatalf("instantiate: %v", err)
			}
			if err := d.Reseed(nil); err != nil {
				t.Fatalf("reseed: %v", err)
			}
			out := make([]byte, 128)
			if err := d.generateInto(out); err != nil {
				t.Fatalf("generate 1: %v", err)
			}
			if err := d.generateInto(out); err != nil {
				t.Fatalf("generate 2: %v", err)
			}
			if want := mustHex(t, tc.expected); !bytes.Equal(out, want) {
				t.Fatalf("KAT mismatch\n got %x\nwant %x", out, want)
			}
			if d.ReseedCount() != 1 {
				t.Fatalf("ReseedCount = %d, want 1", d.ReseedCount())
			}
		})
	}
}

// drbgForTest devuelve un DRBG con entropía scripted suficiente para `reseeds`
// reseeds además de la instanciación.
func drbgForTest(t *testing.T, reseeds int) *HMACDRBG {
	t.Helper()
	chunks := [][]byte{make([]byte, 48)}
	for i := 0; i < reseeds; i++ {
		c := make([]byte, 32)
		c[0] = byte(i + 1)
		chunks = append(chunks, c)
	}
	for i := range chunks[0] {
		chunks[0][i] = 0xAB
	}
	d, err := NewHMACDRBG(&scriptedEntropy{chunks: chunks}, []byte("vg-racegen/test"))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	return d
}

// TestHMACDRBG_SourceContract verifica el contrato Source: conteo de
// generación, continuidad del buffer entre chunks y determinismo con la
// misma entropía.
func TestHMACDRBG_SourceContract(t *testing.T) {
	a := drbgForTest(t, 0)
	b := drbgForTest(t, 0)

	const n = 3 * drbgChunk / 4 // cruza fronteras de chunk
	for i := 0; i < n; i++ {
		va, vb := a.NextUint32(), b.NextUint32()
		if va != vb {
			t.Fatalf("draw %d: determinismo roto con misma entropía: %08x != %08x", i, va, vb)
		}
	}
	if a.GenerationCount() != uint64(n) {
		t.Fatalf("GenerationCount = %d, want %d", a.GenerationCount(), n)
	}

	var src Source = a // compila ⇒ satisface la interfaz
	_ = src.NextUint32()
}

// TestHMACDRBG_AutoReseed verifica que el reseed automático se dispara tras
// drbgReseedInterval peticiones de generación y consume entropía nueva.
func TestHMACDRBG_AutoReseed(t *testing.T) {
	d := drbgForTest(t, 1) // entropía para exactamente 1 reseed
	out := make([]byte, 32)
	for i := 0; i < drbgReseedInterval; i++ {
		if err := d.generateInto(out); err != nil {
			t.Fatalf("generate %d: %v", i, err)
		}
	}
	if d.ReseedCount() != 0 {
		t.Fatalf("reseed prematuro: ReseedCount=%d tras %d generates", d.ReseedCount(), drbgReseedInterval)
	}
	// La petición drbgReseedInterval+1 debe reseedear primero.
	if err := d.generateInto(out); err != nil {
		t.Fatalf("generate post-interval: %v", err)
	}
	if d.ReseedCount() != 1 {
		t.Fatalf("ReseedCount = %d, want 1", d.ReseedCount())
	}
	// Sin más entropía scripted, el siguiente ciclo de reseed debe fallar
	// con error explícito (fail-safe R11), nunca degradar. La petición que
	// disparó el reseed ya contó como la 1ª del intervalo nuevo, de ahí el -1.
	for i := 0; i < drbgReseedInterval-1; i++ {
		if err := d.generateInto(out); err != nil {
			t.Fatalf("generate %d (2º ciclo): %v", i, err)
		}
	}
	if err := d.generateInto(out); err == nil {
		t.Fatal("esperaba error de entropía agotada, generate aceptó")
	}
}

// TestHMACDRBG_ReseedDiscardsBuffer verifica que un Reseed explícito invalida
// la salida pre-generada bajo el estado anterior.
func TestHMACDRBG_ReseedDiscardsBuffer(t *testing.T) {
	a := drbgForTest(t, 1)
	b := drbgForTest(t, 1)

	_ = a.NextUint32() // llena el buffer de a
	_ = b.NextUint32()
	if err := a.Reseed(nil); err != nil {
		t.Fatalf("reseed: %v", err)
	}
	if err := b.Reseed(nil); err != nil {
		t.Fatalf("reseed: %v", err)
	}
	// Tras el reseed ambos deben seguir sincronizados (misma entropía) y el
	// buffer debe haberse regenerado bajo el estado nuevo.
	for i := 0; i < 16; i++ {
		if va, vb := a.NextUint32(), b.NextUint32(); va != vb {
			t.Fatalf("draw %d post-reseed: %08x != %08x", i, va, vb)
		}
	}
	if a.bufN >= drbgChunk {
		t.Fatal("buffer no regenerado tras reseed")
	}
}

// TestHMACDRBG_NilEntropy verifica el rechazo de una EntropySource nil.
func TestHMACDRBG_NilEntropy(t *testing.T) {
	if _, err := NewHMACDRBG(nil, nil); err == nil {
		t.Fatal("esperaba error con EntropySource nil")
	}
}

// TestHMACDRBG_UniformitySmoke: chi² grueso sobre 1M de draws en 256 celdas.
// No sustituye a la batería de la sumisión — detecta roturas groseras del
// pipeline de bytes (endianness, offsets de buffer).
func TestHMACDRBG_UniformitySmoke(t *testing.T) {
	d, err := NewHMACDRBG(CryptoEntropy{}, []byte("smoke"))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	const draws = 1_000_000
	var counts [256]int
	for i := 0; i < draws; i++ {
		counts[d.NextUint32()>>24]++
	}
	expected := float64(draws) / 256
	var chi2 float64
	for _, c := range counts {
		diff := float64(c) - expected
		chi2 += diff * diff / expected
	}
	// df=255; p=0.0001 ⇒ χ² ≈ 341.5. Umbral holgado anti-flaky.
	if chi2 > 360 {
		t.Fatalf("chi² = %.1f sobre df=255 — distribución rota", chi2)
	}
}
