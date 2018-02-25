package zstd

import (
	"flag"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var writeOut = flag.Bool("w", false, "write to output files")

func TestReaderGood(t *testing.T) {
	names, _ := filepath.Glob(filepath.Join("testdata", "good", "*.zst"))
	for _, name := range names {
		wantPath := strings.TrimSuffix(name, ".zst")
		testName := filepath.Base(wantPath)
		t.Run(testName, func(t *testing.T) {
			testReadGood(t, wantPath)
		})
	}
}

func testReadGood(t *testing.T, wantPath string) {
	f, err := os.Open(wantPath + ".zst")
	if err != nil {
		t.Fatal(err)
	}

	r := NewReader(f)
	got, err := ioutil.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}

	checkMatch(t, wantPath, string(got))
}

func TestReaderBad(t *testing.T) {
	names, _ := filepath.Glob(filepath.Join("testdata", "bad", "*.zst"))
	for _, name := range names {
		wantPath := strings.TrimSuffix(name, ".zst")
		testName := filepath.Base(wantPath)
		t.Run(testName, func(t *testing.T) {
			testReadBad(t, wantPath)
		})
	}
}

func testReadBad(t *testing.T, wantPath string) {
	f, err := os.Open(wantPath + ".zst")
	if err != nil {
		t.Fatal(err)
	}

	r := NewReader(f)
	_, err = ioutil.ReadAll(r)
	if err == nil {
		t.Fatal("got a nil error")
	}

	// easier to read/modify with a newline
	got := err.Error() + "\n"
	checkMatch(t, wantPath, got)
}

func checkMatch(t *testing.T, wantPath, got string) {
	if *writeOut {
		err := ioutil.WriteFile(wantPath, []byte(got), 0644)
		if err != nil {
			t.Fatal(err)
		}
	}
	wantBs, err := ioutil.ReadFile(wantPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	want := string(wantBs)
	if got != want {
		t.Fatalf("\ngot:  %q\nwant: %q", got, want)
	}
}
