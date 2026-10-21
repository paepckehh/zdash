package pcscid

// Hash stability contract.
//
// Btags and reader tags are long lived identifiers: consumers store
// them, chrony registers reader tags, kiosk pages key on them. The
// digest input, the domain strings, the alphabet and the truncation
// are therefore frozen behavior, not implementation details.
//
// Never change an existing domain, separator, alphabet, dash layout,
// digest order, card type name or reader name normalization. A new
// identity scheme gets a NEW domain version and a NEW function, the
// old function keeps serving the old domain forever. Every new card
// type, reader fact or reader name shape must extend these golden
// tables in the same change. Weakening a golden test is a breaking
// release, never a test fix.

import (
	"encoding/hex"
	"strings"
	"testing"
)

var hashGoldenUID = []byte{0x04, 0x11, 0x22, 0x33}

// contactlessATR builds a parseable PC/SC part 3 pseudo ATR carrying
// the given standard and card type byte.
func contactlessATR(standard, cardType byte) []byte {
	historical := []byte{
		0x80, 0x4F, 0x0C,
		0xA0, 0x00, 0x00, 0x03, 0x06,
		standard, 0x00, cardType,
		0x00, 0x00, 0x00, 0x00,
	}
	atr := append([]byte{0x3B, 0x8F, 0x80, 0x01}, historical...)
	var tck byte
	for _, b := range atr[1:] {
		tck ^= b
	}
	return append(atr, tck)
}

// TestBtagCardTypesGolden pins every PC/SC part 3 card type name and
// the btag the type produces for a fixed UID. A renamed, removed or
// reordered entry changes card identity forever: this table is the
// compatibility boundary. A newly added card type must add a row.
func TestBtagCardTypesGolden(t *testing.T) {
	t.Parallel()
	golden := map[byte]struct {
		name string
		btag string
	}{
		0x00: {"contactless, card name not given", "gdu-bz1-xxr0"},
		0x01: {"mifare classic 1k", "r3v-401-5gmr"},
		0x02: {"mifare classic 4k", "igj-p74-zceq"},
		0x03: {"mifare ultralight", "l0s-pg6-fq4c"},
		0x04: {"sle55r", "p91-lfz-w3sc"},
		0x06: {"sr176", "5e7-pgi-z08z"},
		0x07: {"sri x4k", "fjn-nw0-gnam"},
		0x08: {"at88rf020", "52w-5fh-ghg5"},
		0x09: {"at88sc0204crf", "q66-r1o-6csl"},
		0x0A: {"at88sc0808crf", "sao-68p-335k"},
		0x0B: {"at88sc1616crf", "drk-qmz-ng05"},
		0x0C: {"at88sc3216crf", "8qg-7pr-rkzj"},
		0x0D: {"at88sc6416crf", "huo-kym-cyik"},
		0x0E: {"srf55v10p", "a5c-vbx-msci"},
		0x0F: {"srf55v02p", "ks1-mwl-w78x"},
		0x10: {"srf55v10s", "pw4-9oj-5d8e"},
		0x11: {"srf55v02s", "ej4-5ss-sd6a"},
		0x12: {"tag it", "ih0-k0o-slux"},
		0x13: {"lri512", "rq0-hf4-76ed"},
		0x14: {"icodesli", "q0v-5zb-7y6o"},
		0x15: {"tempsens", "2co-ofc-at53"},
		0x16: {"i-code1", "vqf-f8j-7ry6"},
		0x17: {"picopass 2k", "68j-p7y-libn"},
		0x18: {"picopass 2ks", "vb9-lxn-2eyw"},
		0x19: {"picopass 16k", "v64-46i-l8jq"},
		0x1A: {"picopass 16ks", "5v6-jty-voi3"},
		0x1B: {"picopass 16k(8x2)", "obr-q21-hwul"},
		0x1C: {"picopass 16ks(8x2)", "m53-qsk-2b1x"},
		0x1D: {"picopass 32ks(16+16)", "e0d-3g8-69j5"},
		0x1E: {"picopass 32ks(16+8x2)", "nrk-qvb-rt49"},
		0x1F: {"picopass 32ks(8x2+16)", "3ne-vlm-5a0y"},
		0x20: {"picopass 32ks(8x2+8x2)", "74j-meq-t07m"},
		0x21: {"lri64", "e89-ewh-0ti3"},
		0x22: {"i.code uid", "yix-0uv-9x5g"},
		0x23: {"i.code epc", "bw5-eer-h6m2"},
		0x24: {"lri12", "eu8-1q6-8ly9"},
		0x25: {"lri128", "77d-qe4-6bc7"},
		0x26: {"mifare mini", "rxk-zmn-f1yf"},
		0x27: {"my-d move (sle 66r01p)", "hte-5io-3d3h"},
		0x28: {"my-d nfc (sle 66rxxp)", "ctp-mwd-ltqn"},
		0x29: {"my-d proximity 2 (sle 66rxxs)", "o18-rl1-hd2g"},
		0x2A: {"my-d proximity enhanced (sle 55rxxe)", "u0f-m57-h3vo"},
		0x2B: {"my-d light (srf 55v01p)", "r31-t63-fqlp"},
		0x2C: {"pjm stack tag (srf 66v10st)", "7tr-bo3-z602"},
		0x2D: {"pjm item tag (srf 66v10it)", "67v-eha-q7cu"},
		0x2E: {"pjm light (srf 66v01st)", "gpu-j1b-nggy"},
		0x2F: {"jewel tag", "em5-zfl-xki7"},
		0x30: {"topaz nfc tag", "t0q-1dn-i9yg"},
		0x31: {"at88sc0104crf", "680-gjm-qkt4"},
		0x32: {"at88sc0404crf", "5l3-5ax-2xee"},
		0x33: {"at88rf01c", "t0s-9ui-ptar"},
		0x34: {"at88rf04c", "gk6-pcd-qwsx"},
		0x35: {"i-code sl2", "c0e-zps-so1r"},
		0x36: {"mifare plus sl1 2k", "63n-od2-ri6z"},
		0x37: {"mifare plus sl1 4k", "ek7-w3k-effs"},
		0x38: {"mifare plus sl2 2k", "it3-pck-qknq"},
		0x39: {"mifare plus sl2 4k", "tj0-p8s-kr85"},
		0x3A: {"mifare ultralight c", "hez-ws4-hc8f"},
		0x3B: {"felica", "vzc-ajo-vzea"},
		0x3C: {"melexis sensor tag (mlx90129)", "n36-jgx-ufll"},
		0x3D: {"mifare ultralight ev1", "by1-193-l0z2"},
	}
	if len(golden) != len(pcscCardType) {
		t.Fatalf("golden has %d card types, table has %d: add the new card type with its golden btag",
			len(golden), len(pcscCardType))
	}
	for key, want := range golden {
		name, ok := pcscCardType[key]
		if !ok {
			t.Errorf("card type 0x%02X is in the golden table but missing from pcscCardType", key)
			continue
		}
		if name != want.name {
			t.Errorf("card type 0x%02X = %q, want %q: renaming a type changes every stored btag",
				key, name, want.name)
		}
		atr := contactlessATR(0x03, key)
		if got := DetectType(atr); got != want.name {
			t.Errorf("DetectType(0x%02X) = %q, want %q", key, got, want.name)
		}
		if got := Btag(want.name, hashGoldenUID); got != want.btag {
			t.Errorf("Btag(%q) = %q, want %q", want.name, got, want.btag)
		}
	}
}

// TestBtagStandardsGolden pins the standard byte fallbacks and the
// two generic type names that carry no model information.
func TestBtagStandardsGolden(t *testing.T) {
	t.Parallel()
	golden := []struct {
		standard byte
		name     string
		btag     string
	}{
		{0x00, "rfid, no standard given", "gjn-c4r-8x14"},
		{0x01, "rfid, iso 14443 type a part 1", "gn3-4n5-jk46"},
		{0x02, "rfid, iso 14443 type a part 2", "unb-9un-p5rt"},
		{0x03, "iso 14443 type a, pc/sc part 3", "12a-e9y-m6pw"},
	}
	if len(golden) != len(pcscStandard) {
		t.Fatalf("golden has %d standards, table has %d: add the new standard with its golden btag",
			len(golden), len(pcscStandard))
	}
	for _, g := range golden {
		if got, ok := pcscStandard[g.standard]; !ok || got != g.name {
			t.Errorf("pcscStandard[0x%02X] = %q (present %v), want %q", g.standard, got, ok, g.name)
		}
		if g.standard != 0x03 {
			if got := DetectType(contactlessATR(g.standard, 0x00)); got != g.name {
				t.Errorf("DetectType(standard 0x%02X) = %q, want %q", g.standard, got, g.name)
			}
		}
		if got := Btag(g.name, hashGoldenUID); got != g.btag {
			t.Errorf("Btag(%q) = %q, want %q", g.name, got, g.btag)
		}
	}
	fallbacks := []struct {
		name string
		atr  []byte
		btag string
	}{
		{"iso 14443 type a, pc/sc part 3", contactlessATR(0x03, 0x42), "12a-e9y-m6pw"},
		{"pc/sc contactless, unspecified", contactlessATR(0x04, 0x00), "wta-p43-eh1q"},
		{"unknown", []byte{0x3B, 0x00}, "haw-wwk-ygwv"},
	}
	for _, f := range fallbacks {
		if got := DetectType(f.atr); got != f.name {
			t.Errorf("DetectType(% X) = %q, want %q", f.atr, got, f.name)
		}
		if got := Btag(f.name, hashGoldenUID); got != f.btag {
			t.Errorf("Btag(%q) = %q, want %q", f.name, got, f.btag)
		}
	}
}

// TestBtagKnownATRGolden pins every full ATR lookup and the btag of
// its type name. A changed hex key or type string is a breaking
// identity change for that card family.
func TestBtagKnownATRGolden(t *testing.T) {
	t.Parallel()
	golden := []struct {
		atr  string
		name string
		btag string
	}{
		{"3B8480018082900097", "german eid/passport (npa)", "p5n-pcp-u5y4"},
		{"3B8D80018073C021C057597562694B65FF7F", "yubikey 5 nfc", "kk8-em8-d3up"},
		{"3B8580015A4356445659", "deutschlandticket (vdv-ka)", "xqo-qrc-z9ml"},
	}
	if len(golden) != len(knownATR) {
		t.Fatalf("golden has %d known ATRs, table has %d: add the new ATR with its golden btag",
			len(golden), len(knownATR))
	}
	for _, g := range golden {
		if name, ok := knownATR[g.atr]; !ok || name != g.name {
			t.Errorf("knownATR[%s] = %q (present %v), want %q", g.atr, name, ok, g.name)
		}
		if got := DetectType(mustHex(t, g.atr)); got != g.name {
			t.Errorf("DetectType(%s) = %q, want %q", g.atr, got, g.name)
		}
		if got := Btag(g.name, hashGoldenUID); got != g.btag {
			t.Errorf("Btag(%q) = %q, want %q", g.name, got, g.btag)
		}
	}
}

// TestBtagUIDShapeGolden pins all UID lengths the PC/SC pseudo APDU
// may serve (4, 7 and 10 bytes) plus the empty edge case. The UID is
// hashed as raw bytes, never as text or hex.
func TestBtagUIDShapeGolden(t *testing.T) {
	t.Parallel()
	golden := []struct {
		name     string
		cardType string
		uid      []byte
		want     string
	}{
		{"4 byte uid", "mifare classic 1k", []byte{0x04, 0x11, 0x22, 0x33}, "r3v-401-5gmr"},
		{"7 byte uid", "mifare ultralight", []byte{0x04, 0xA1, 0xB2, 0xC3, 0xD4, 0xE5, 0xF6}, "w8t-zl0-hvys"},
		{"10 byte uid", "mifare classic 1k", []byte{0x04, 0xA1, 0xB2, 0xC3, 0xD4, 0xE5, 0xF6, 0x17, 0x28, 0x39}, "6gh-6gn-fwep"},
		{"empty", "", nil, "a3q-53z-if6k"},
	}
	for _, g := range golden {
		if got := Btag(g.cardType, g.uid); got != g.want {
			t.Errorf("Btag(%q, %s) = %q, want %q", g.cardType, g.name, got, g.want)
		}
	}
}

// TestBtagFormatGolden pins the encoding contract: the alphabet order,
// the modulo mapping and the dash positions. A change here shifts
// every btag and reader tag ever issued.
func TestBtagFormatGolden(t *testing.T) {
	t.Parallel()
	if btagAlphabet != "0123456789abcdefghijklmnopqrstuvwxyz" {
		t.Fatalf("btagAlphabet = %q, want the frozen order", btagAlphabet)
	}
	sum := make([]byte, 38)
	for i := range sum {
		sum[i] = byte(i)
	}
	if got, want := btagFormat(sum, 38, 3, 6), "012-345-6789abcdefghijklmnopqrstuvwxyz01"; got != want {
		t.Errorf("btagFormat = %q, want %q", got, want)
	}
	if got, want := btagFormat(sum, 10, 3, 6), "012-345-6789"; got != want {
		t.Errorf("btagFormat btag shape = %q, want %q", got, want)
	}
	if got, want := btagFormat(sum, 8, 2, 6), "01-2345-67"; got != want {
		t.Errorf("btagFormat reader shape = %q, want %q", got, want)
	}
}

// TestHashDomainConstantsGolden pins the literal domain strings and
// the machine unit kind prefixes. They are not tunable.
func TestHashDomainConstantsGolden(t *testing.T) {
	t.Parallel()
	domains := []struct {
		name string
		got  string
		want string
	}{
		{"btag", btagDomain, "pcscid/v1|"},
		{"reader model", readerModelDomain, "pcscid/reader/v1|"},
		{"reader serial", readerSerialDomain, "pcscid/reader/v2|"},
		{"reader port", readerPortDomain, "pcscid/reader/v3|"},
		{"reader machine", readerMachineDomain, "pcscid/reader/m1|"},
		{"separator", identitySeparator, "|"},
		{"serial unit prefix", readerUnitSerialPrefix, "serial:"},
		{"port unit prefix", readerUnitPortPrefix, "port:"},
		{"model unit", readerUnitModel, "model"},
	}
	for _, d := range domains {
		if d.got != d.want {
			t.Errorf("%s domain = %q, want %q", d.name, d.got, d.want)
		}
	}
}

// TestHashDigestsGolden pins the full SHA-256 digest of every domain,
// not only the visible truncated tag. The truncation hides most of
// the digest from the user facing goldens; this table catches any
// change in domain order, separators or input encoding.
func TestHashDigestsGolden(t *testing.T) {
	t.Parallel()
	golden := []struct {
		name  string
		parts [][]byte
		want  string
	}{
		{
			"btag",
			[][]byte{[]byte("pcscid/v1|"), []byte("mifare classic 1k"), []byte("|"), hashGoldenUID},
			"1b93d34cd84971583a63cd8ef946f5a953410a020ba742990be376640873f450",
		},
		{
			"reader model",
			[][]byte{[]byte("pcscid/reader/v1|"), []byte("ACS ACR122U")},
			"623f8da563a47e95fd8af0f6099d39d546b1c8217cf981e7ebe989912171151f",
		},
		{
			"reader serial",
			[][]byte{[]byte("pcscid/reader/v2|"), []byte("ACS ACR122U"), []byte("|"), []byte("A001")},
			"c1f88c36c4f56b98082c2d29f61dee61d28411a0d59d870c6dcfdfb69add693d",
		},
		{
			"reader port",
			[][]byte{[]byte("pcscid/reader/v3|"), []byte("ACS ACR122U"), []byte("|"), []byte("1-4.2")},
			"9eec007c80f217025fceaa248cf1a836ef06524971f0c20f0a9c3ae50b404b5c",
		},
		{
			"reader machine serial",
			[][]byte{[]byte("pcscid/reader/m1|"), []byte("ACS ACR122U"), []byte("|"), []byte("serial:A001"), []byte("|"), []byte("aa:bb:cc:dd:ee:01")},
			"137efeac4b530be184e13ac9251775bbf2f5060e23e13cfd4ce4694c15623cfe",
		},
		{
			"reader machine port",
			[][]byte{[]byte("pcscid/reader/m1|"), []byte("ACS ACR122U"), []byte("|"), []byte("port:1-4.2"), []byte("|"), []byte("aa:bb:cc:dd:ee:01")},
			"1f355e6bc8ba098d23df913645709bb8a768eb255f891036cf8974655f67079c",
		},
		{
			"reader machine model",
			[][]byte{[]byte("pcscid/reader/m1|"), []byte("ACS ACR122U"), []byte("|"), []byte("model"), []byte("|"), []byte("aa:bb:cc:dd:ee:01")},
			"504b6e3c7e941b30325751f73975374d4fcdbd34f220c95218b6554f94df958c",
		},
	}
	for _, g := range golden {
		sum := digestID(g.parts...)
		if got := hex.EncodeToString(sum[:]); got != g.want {
			t.Errorf("%s digest = %s, want %s", g.name, got, g.want)
		}
	}
}

// TestNormalizeReaderNameGolden pins the volatile hotplug suffix
// stripping for every shape that has ever been observed or is
// plausible from pcscd. The normalized name is the hash input, so a
// changed edge case changes the reader tag of the affected readers.
func TestNormalizeReaderNameGolden(t *testing.T) {
	t.Parallel()
	golden := []struct {
		reader    string
		norm      string
		tag       string
		serialTag string
	}{
		{"ACS ACR122U 01 00 00", "ACS ACR122U", "qr-xlrk-i5", "dw-wigt-z8"},
		{"ACS ACR122U 02 00 00", "ACS ACR122U", "qr-xlrk-i5", "dw-wigt-z8"},
		{"ACS ACR122U 01 01 00", "ACS ACR122U", "qr-xlrk-i5", "dw-wigt-z8"},
		{"ACS ACR122U 05 01 00", "ACS ACR122U", "qr-xlrk-i5", "dw-wigt-z8"},
		{"ACS ACR122U", "ACS ACR122U", "qr-xlrk-i5", "dw-wigt-z8"},
		{"SCM Microsystems Inc. SCL011", "SCM Microsystems Inc. SCL011", "nt-1oxi-df", "ev-tx10-xu"},
		{"SCM Microsystems Inc. SCL011 00 00", "SCM Microsystems Inc. SCL011", "nt-1oxi-df", "ev-tx10-xu"},
		{"Identiv CLOUD 3700 F Contactless Reader 00 00", "Identiv CLOUD 3700 F Contactless Reader", "02-xqpw-am", "0q-ufxi-er"},
		{"reader 123", "reader 123", "b4-loap-hy", "3k-dxjw-n2"},
		{"reader 123 4", "reader 123", "b4-loap-hy", "3k-dxjw-n2"},
		{"reader 123 04", "reader 123", "b4-loap-hy", "3k-dxjw-n2"},
		{" 01", "", "6v-gokn-2o", "l4-1jt2-gm"},
		{"123", "123", "b9-acjk-pf", "jf-xj6p-j3"},
		{"a 1 2 3", "a", "10-5syb-nv", "2v-2bca-eo"},
		{"a 123", "a 123", "yv-jo4j-ly", "5y-o8o0-v4"},
		{"a 001", "a 001", "0f-3k8l-b9", "1h-xbtm-8v"},
		{"", "", "6v-gokn-2o", "l4-1jt2-gm"},
	}
	for _, g := range golden {
		if got := normalizeReaderName(g.reader); got != g.norm {
			t.Errorf("normalizeReaderName(%q) = %q, want %q", g.reader, got, g.norm)
		}
		if got := ReaderTag(g.reader); got != g.tag {
			t.Errorf("ReaderTag(%q) = %q, want %q", g.reader, got, g.tag)
		}
		if got := ReaderTagWithSerial(g.reader, "A001"); got != g.serialTag {
			t.Errorf("ReaderTagWithSerial(%q, A001) = %q, want %q", g.reader, got, g.serialTag)
		}
	}
}

// TestReaderTagUnitTierGolden pins the tier precedence and the
// machine mixed derivation for every combination of unit facts.
func TestReaderTagUnitTierGolden(t *testing.T) {
	t.Parallel()
	const (
		reader  = "ACS ACR122U 01 00 00"
		serial  = "A001"
		port    = "1-4.2"
		machine = "aa:bb:cc:dd:ee:01"
	)
	// Serial wins over port, machine empty falls back to the unit tag.
	if got, want := ReaderTagWithUnit(reader, serial, port), ReaderTagWithSerial(reader, serial); got != want {
		t.Errorf("serial+port = %q, want serial tier %q", got, want)
	}
	if got, want := ReaderTagWithUnit(reader, serial, port), "dw-wigt-z8"; got != want {
		t.Errorf("serial+port = %q, want %q", got, want)
	}
	if got, want := ReaderTagWithUnit(reader, "", port), "ek-0gkq-n2"; got != want {
		t.Errorf("port tier = %q, want %q", got, want)
	}
	if got, want := ReaderTagWithUnit(reader, "", ""), ReaderTag(reader); got != want {
		t.Errorf("no unit fact = %q, want model tier %q", got, want)
	}
	if got, want := ReaderTagWithMachine(reader, serial, port, ""), ReaderTagWithUnit(reader, serial, port); got != want {
		t.Errorf("empty machine = %q, want unit tag %q", got, want)
	}
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"machine serial", ReaderTagWithMachine(reader, serial, "", machine), "ji-2s3b-b9"},
		{"machine port", ReaderTagWithMachine(reader, "", port, machine), "vh-mzk6-9x"},
		{"machine model", ReaderTagWithMachine(reader, "", "", machine), "83-2oi4-rc"},
		{"machine serial+port", ReaderTagWithMachine(reader, serial, port, machine), "ji-2s3b-b9"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	// The unit kind prefix keeps a serial and a port of the same text
	// in different hash inputs, so they cannot collide.
	if ReaderTagWithMachine(reader, "X", "", machine) == ReaderTagWithMachine(reader, "", "X", machine) {
		t.Error("serial:X and port:X collided in the machine domain")
	}
}

// TestCardTypeNamesAreHashSafe guards the separator contract: the
// card type and the tag share one domain separated hash input, so a
// card type name containing the separator would make two different
// (type, tag) pairs hash identically.
func TestCardTypeNamesAreHashSafe(t *testing.T) {
	t.Parallel()
	names := make([]string, 0, len(pcscCardType)+len(pcscStandard)+len(knownATR))
	for _, name := range pcscCardType {
		names = append(names, name)
	}
	for _, name := range pcscStandard {
		names = append(names, name)
	}
	for _, name := range knownATR {
		names = append(names, name)
	}
	for _, name := range []string{"unknown", "pc/sc contactless, unspecified", "iso 14443 type a, pc/sc part 3"} {
		names = append(names, name)
	}
	for _, name := range names {
		if strings.Contains(name, identitySeparator) {
			t.Errorf("card type %q contains the hash separator %q; it would break type/tag separation",
				name, identitySeparator)
		}
	}
}

// TestReaderTagShapeGolden pins the exact tag shapes: btag
// xxx-xxx-xxxx and reader tag xx-xxxx-xx, digits and lower case only.
func TestReaderTagShapeGolden(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, got, want string }{
		{"btag", Btag("mifare classic 1k", hashGoldenUID), "r3v-401-5gmr"},
		{"reader model", ReaderTag("ACS ACR122U 01 00 00"), "qr-xlrk-i5"},
		{"reader serial", ReaderTagWithSerial("ACS ACR122U 01 00 00", "A001"), "dw-wigt-z8"},
		{"reader port", ReaderTagWithUnit("ACS ACR122U 01 00 00", "", "1-4.2"), "ek-0gkq-n2"},
		{"reader machine", ReaderTagWithMachine("ACS ACR122U 01 00 00", "A001", "", "aa:bb:cc:dd:ee:01"), "ji-2s3b-b9"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
		if strings.ToLower(c.got) != c.got {
			t.Errorf("%s = %q, must stay lower case", c.name, c.got)
		}
	}
}
