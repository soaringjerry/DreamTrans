package main

import (
    "bufio"
    "context"
    "encoding/csv"
    "flag"
    "fmt"
    "io"
    "log"
    "os"
    "strings"
    "time"

    "github.com/dreamtrans/backend/internal/dict"
)

func main() {
    inPath := flag.String("in", "", "input CSV file (EnWords.csv)")
    outPath := flag.String("out", "./dict.db", "output SQLite path")
    colWord := flag.String("col-word", "word", "CSV column name for the word")
    colDef := flag.String("col-def", "definition", "CSV column name for the definition/释义")
    colPhon := flag.String("col-phon", "phonetic", "CSV column name for phonetic/音标 (optional)")
    colPOS := flag.String("col-pos", "pos", "CSV column name for part-of-speech/词性 (optional)")
    sep := flag.String("sep", ",", "CSV separator ("," or \t)")
    flag.Parse()

    if strings.TrimSpace(*inPath) == "" {
        log.Fatal("-in is required (CSV path)")
    }
    f, err := os.Open(*inPath)
    if err != nil { log.Fatalf("open input: %v", err) }
    defer f.Close()

    s, err := dict.Open(*outPath)
    if err != nil { log.Fatalf("open sqlite: %v", err) }
    defer s.Close()

    r := csv.NewReader(bufio.NewReader(f))
    if *sep == "\t" { r.Comma = '\t' }
    r.FieldsPerRecord = -1
    header, err := r.Read()
    if err != nil { log.Fatalf("read header: %v", err) }
    idx := func(name string) int {
        if name == "" { return -1 }
        ln := strings.ToLower(strings.TrimSpace(name))
        for i, h := range header {
            hh := strings.ToLower(strings.TrimSpace(h))
            if hh == ln { return i }
        }
        return -1
    }
    iw := idx(*colWord)
    idf := idx(*colDef)
    iph := idx(*colPhon)
    ipos := idx(*colPOS)
    if iw < 0 || idf < 0 {
        log.Fatalf("required columns not found. got headers=%v need word=%q def=%q (case-insensitive)", header, *colWord, *colDef)
    }

    ctx := context.Background()
    count := 0
    start := time.Now()
    for {
        rec, err := r.Read()
        if err != nil {
            if err == io.EOF { break }
            log.Fatalf("read: %v", err)
        }
        get := func(i int) string { if i >= 0 && i < len(rec) { return strings.TrimSpace(rec[i]) } ; return "" }
        w := get(iw)
        d := get(idf)
        if w == "" || d == "" { continue }
        e := &dict.Entry{Word: w, Definition: d, Phonetic: get(iph), POS: get(ipos)}
        if err := s.InsertOrReplace(ctx, e); err != nil { log.Fatalf("insert: %v", err) }
        count++
        if count%10000 == 0 {
            elapsed := time.Since(start)
            fmt.Printf("Imported %d rows in %s\n", count, elapsed.Truncate(time.Second))
        }
    }
    fmt.Printf("Done. Imported %d rows. Output: %s\n", count, *outPath)
}
