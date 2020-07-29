package main

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/jessevdk/go-flags"
	"github.com/mb0/diff"
)

// base model of the patch file that's JSON encoded and then compressed
type Patch struct {
	Hash          []byte         `json:"H"`
	Modifications []Modification `json:"M"`
}

// each modification with a slim json output
type Modification struct {
	Location int    `json:"L,omitempty"`
	Insert   []byte `json:"I,omitempty"`
	Delete   int    `json:"D,omitempty"`
}

type PositionalFiles struct {
	Action    string `positional-arg-name:"ACTION" required:"true"`
	BaseFile  string `positional-arg-name:"BASE_FILE" required:"true"`
	OtherFile string `positional-arg-name:"OTHER_FILE" required:"true"`
}

type Arguments struct {
	Output     string          `short:"o" long:"out" description:"output name"`
	Force      bool            `short:"f" long:"force" description:"force the patch even if target integrity check fails"`
	Positional PositionalFiles `positional-args:"true"`
}

var args Arguments

func printExtendedUsage() {
	// adding details about the ACTION variable
	fmt.Println("Action Options:")
	fmt.Println("  diff          Create a diff file that can convert BASE_FILE to OTHER_FILE")
	fmt.Println("  patch         Update the BASE_FILE using the diff file in OTHER_FILE")
}

func main() {
	_, err := flags.Parse(&args)
	if flags.WroteHelp(err) {
		printExtendedUsage()
		return
	} else if err != nil {
		// whoops
		panic(err)
	}

	// determine which action to do and do it
	switch strings.ToLower(args.Positional.Action) {
	case "diff":
		buildDiff()
	case "patch":
		applyPatch()
	default:
		// don't know what to do
		fmt.Printf("unknown ACTION: %s, must be either DIFF or PATCH\n", args.Positional.Action)
		os.Exit(1)
	}
}

func buildDiff() {
	// the base file is the file that we will later apply this diff to
	f, err := os.Open(args.Positional.BaseFile)
	if err != nil {
		panic(err)
	}

	defer f.Close()

	// hash the file as we read it for the integrity check
	hasher := sha256.New()
	t := io.TeeReader(f, hasher)

	one, err := ioutil.ReadAll(t)
	if err != nil {
		panic(err)
	}

	h := hasher.Sum(nil)

	two, err := ioutil.ReadFile(args.Positional.OtherFile)
	if err != nil {
		panic(err)
	}

	changes := diff.Bytes(one, two) // where the magic happens

	patch := Patch{
		Hash:          h,
		Modifications: make([]Modification, len(changes)),
	}
	for i, c := range changes { // where the other magic happens
		mod := Modification{
			Location: c.A,
			Delete:   c.Del,
		}

		if c.Ins != 0 {
			// instead of storing how many bytes come from the other file,
			// store the actual bytes (will be base64 in JSON)
			mod.Insert = two[c.B : c.B+c.Ins]
		}

		patch.Modifications[i] = mod
	}

	output, err := json.Marshal(patch)
	if err != nil {
		panic(err)
	}

	filename := args.Output

	if len(filename) == 0 {
		_, filename = filepath.Split(args.Positional.BaseFile)
		filename = filename + ".patch"
	}

	out, err := os.Create(filename)
	if err != nil {
		panic(err)
	}

	defer out.Close()

	// compress it
	z := zlib.NewWriter(out)

	_, err = z.Write(output)
	if err != nil {
		panic(err)
	}

	err = z.Close()
	if err != nil {
		panic(err)
	}
}

func applyPatch() {
	// the base file will receive modifications
	f, err := os.Open(args.Positional.BaseFile)
	if err != nil {
		panic(err)
	}

	defer f.Close()

	// hash to verify
	hasher := sha256.New()
	t := io.TeeReader(f, hasher)

	base, err := ioutil.ReadAll(t)
	if err != nil {
		panic(err)
	}

	h := hasher.Sum(nil)

	// the other file should be the patch file
	other, err := os.Open(args.Positional.OtherFile)
	if err != nil {
		panic(err)
	}

	z, err := zlib.NewReader(other)
	if err != nil {
		panic(err)
	}

	rawJson, err := ioutil.ReadAll(z)
	if err != nil {
		panic(err)
	}

	patch := Patch{}

	err = json.Unmarshal(rawJson, &patch)
	if err != nil {
		panic(err)
	}

	// check the hash and stop... unless forced
	if !bytes.Equal(patch.Hash, h) {
		if args.Force {
			fmt.Println("hash mismtach, forcing through it")
		} else {
			fmt.Println("hash mismatch, giving up")
			return
		}
	}

	var output []byte

	index := 0
	for loc := 0; loc < len(base); loc++ {
		if index == len(patch.Modifications) || loc != patch.Modifications[index].Location {
			output = append(output, base[loc])
			continue
		}

		loc += patch.Modifications[index].Delete
		output = append(output, patch.Modifications[index].Insert...)
		index++
		loc--
	}

	filename := args.Output

	if len(filename) == 0 {
		// attempt to remove the file extension from the patch file
		// if the patch file wasn't named with the expected suffix
		// prepend [PATCHED] to the patch file name
		_, patchfilename := filepath.Split(args.Positional.BaseFile)
		filename = strings.TrimSuffix(patchfilename, ".patch")
		if filename == patchfilename {
			filename = "[PATCHED]" + filename
		}
	}

	err = ioutil.WriteFile(filename, output, 0666)
	if err != nil {
		panic(err)
	}
}
