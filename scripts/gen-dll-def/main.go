// Command gen-dll-def reads a PE DLL and writes a module-definition (.def)
// file for its export table to stdout, suitable for feeding to
// llvm-dlltool/zig dlltool to produce an import library.
package main

import (
	"debug/pe"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gen-dll-def <dll>")
		os.Exit(2)
	}
	path := os.Args[1]
	f, err := pe.Open(path)
	check(err)
	defer f.Close()

	var exportDir pe.DataDirectory
	switch oh := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		exportDir = oh.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_EXPORT]
	case *pe.OptionalHeader32:
		exportDir = oh.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_EXPORT]
	default:
		fatal("unsupported optional header")
	}
	if exportDir.VirtualAddress == 0 {
		fatal("no export table")
	}

	raw, err := os.ReadFile(path)
	check(err)
	off := rvaToOffset(f, exportDir.VirtualAddress)

	// The import must use the DLL's internal name from its export directory
	// (e.g. "skia.dll"), which can differ from the on-disk filename.
	internalName := filepath.Base(path)
	if nameRVA := binary.LittleEndian.Uint32(raw[off+12:]); nameRVA != 0 {
		nameOff := rvaToOffset(f, nameRVA)
		end := nameOff
		for raw[end] != 0 {
			end++
		}
		internalName = string(raw[nameOff:end])
	}

	numNames := binary.LittleEndian.Uint32(raw[off+24:])
	namesRVA := binary.LittleEndian.Uint32(raw[off+32:])
	namesOff := rvaToOffset(f, namesRVA)

	names := make([]string, 0, numNames)
	for i := uint32(0); i < numNames; i++ {
		nameRVA := binary.LittleEndian.Uint32(raw[namesOff+int64(i*4):])
		nameOff := rvaToOffset(f, nameRVA)
		end := nameOff
		for raw[end] != 0 {
			end++
		}
		names = append(names, string(raw[nameOff:end]))
	}
	sort.Strings(names)

	w := io.Writer(os.Stdout)
	fmt.Fprintf(w, "LIBRARY %s\nEXPORTS\n", internalName)
	for _, n := range names {
		fmt.Fprintln(w, n)
	}
}

func rvaToOffset(f *pe.File, rva uint32) int64 {
	for _, s := range f.Sections {
		if rva >= s.VirtualAddress && rva < s.VirtualAddress+s.VirtualSize {
			return int64(s.Offset + rva - s.VirtualAddress)
		}
	}
	fatal(fmt.Sprintf("rva 0x%x not in any section", rva))
	return 0
}

func check(err error) {
	if err != nil {
		fatal(err.Error())
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "gen-dll-def:", msg)
	os.Exit(1)
}
