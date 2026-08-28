package history

import (
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

func TestCanonicalRowV1LengthPrefixAndDomains(t *testing.T) {
	row, err := CanonicalRowV1(TextField("a"), NullField(), TextField(""))
	if err != nil {
		t.Fatal(err)
	}
	want := "52534831000000030000000161ffffffff00000000"
	if got := hex.EncodeToString(row); got != want {
		t.Fatalf("canonical row = %s, want %s", got, want)
	}

	row, err = CanonicalRowV1(
		Int64Field(-12), Uint64Field(34), BoolField(true), BoolField(false),
		TimeField(time.Date(2026, 8, 20, 1, 2, 3, 400, time.FixedZone("UTC+8", 8*60*60))),
		BytesField([]byte{0x00, 0xab, 0xff}),
	)
	if err != nil {
		t.Fatal(err)
	}
	// Re-encoding the same instant and values must be byte-identical.
	again, err := CanonicalRowV1(
		TextField("-12"), TextField("34"), TextField("t"), TextField("f"),
		TextField("2026-08-19T17:02:03.0000004Z"), TextField("00abff"),
	)
	if err != nil || string(row) != string(again) {
		t.Fatalf("typed canonical domains changed bytes: equal=%v err=%v", string(row) == string(again), err)
	}
}

func TestChainV1GoldenVectors(t *testing.T) {
	rows := [][]CanonicalField{
		{TextField("instance-a"), Uint64Field(7), BoolField(true)},
		{TextField("instance-b"), NullField(), BoolField(false)},
	}
	var chain ChainV1
	if got := checksumHex(chain.Sum()); got != "0000000000000000000000000000000000000000000000000000000000000000" {
		t.Fatalf("empty chain = %s", got)
	}
	if err := chain.Add(rows[0]...); err != nil {
		t.Fatal(err)
	}
	const singleGolden = "136d4d2c579f30c7ac22bcab7b50a6dbe8c0100f551d39f219b8ad2cb3f9e967"
	if got := checksumHex(chain.Sum()); got != singleGolden {
		t.Fatalf("single chain = %s, want %s", got, singleGolden)
	}
	if err := chain.Add(rows[1]...); err != nil {
		t.Fatal(err)
	}
	const multipleGolden = "1b049d7b8bdcfce3c849b9bbcd34a4c77503e326ec4c1aa61215e5f0d77e86aa"
	if got := checksumHex(chain.Sum()); got != multipleGolden {
		t.Fatalf("multiple chain = %s, want %s", got, multipleGolden)
	}
}

func checksumHex(sum [32]byte) string { return hex.EncodeToString(sum[:]) }

func TestChainV1IsOrderedAndFieldSensitive(t *testing.T) {
	a := []CanonicalField{TextField("a"), Uint64Field(1)}
	b := []CanonicalField{TextField("b"), Uint64Field(2)}
	checksum := func(rows ...[]CanonicalField) [32]byte {
		var chain ChainV1
		for _, row := range rows {
			if err := chain.Add(row...); err != nil {
				t.Fatal(err)
			}
		}
		return chain.Sum()
	}
	baseline := checksum(a, b)
	if baseline == checksum(b, a) {
		t.Fatal("row order did not affect chain")
	}
	if baseline == checksum(a, []CanonicalField{TextField("b"), Uint64Field(3)}) {
		t.Fatal("field change did not affect chain")
	}
}

func TestChainV1RejectsNonCanonicalRows(t *testing.T) {
	valid, err := CanonicalRowV1(TextField("value"))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]byte{
		[]byte("not-a-row"),
		valid[:len(valid)-1],
		append(append([]byte(nil), valid...), 0),
	} {
		var chain ChainV1
		if err := chain.AddCanonicalRow(row); !errors.Is(err, ErrCanonicalRowInvalid) {
			t.Fatalf("AddCanonicalRow(%x) error = %v", row, err)
		}
		if chain.Sum() != ([32]byte{}) {
			t.Fatal("invalid row changed chain state")
		}
	}
}

func TestChainV1MillionRowsDoesNotRetainRows(t *testing.T) {
	var chain ChainV1
	row := []CanonicalField{Uint64Field(0), TextField("fixed")}
	for index := uint64(0); index < 1_000_000; index++ {
		row[0] = Uint64Field(index)
		if err := chain.Add(row...); err != nil {
			t.Fatal(err)
		}
	}
	if chain.Sum() == ([32]byte{}) {
		t.Fatal("non-empty chain remained at H0")
	}
}
