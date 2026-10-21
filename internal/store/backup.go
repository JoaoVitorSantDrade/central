package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// KeepBackups é quantas cópias de cada banco ficam guardadas.
const KeepBackups = 48

// backupStamp é o carimbo (UTC) no nome das cópias: AAAAMMDDTHHMMSS.
const backupStamp = "20060102T150405"

// Backup copia o banco (VACUUM INTO) para dir/base-AAAAMMDDTHHMMSS.db e apaga
// as cópias mais antigas de base além de keep. Duas cópias no mesmo segundo
// ganham um sufixo (-1, -2...) para não se sobrescreverem. Devolve o caminho
// da cópia nova.
func Backup(db *sql.DB, dir, base string, keep int) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format(backupStamp)
	path := filepath.Join(dir, base+"-"+stamp+".db")
	for n := 1; ; n++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		} else if err != nil {
			return "", err
		}
		path = filepath.Join(dir, fmt.Sprintf("%s-%s-%d.db", base, stamp, n))
	}
	if _, err := db.Exec(`VACUUM INTO ?`, path); err != nil {
		return "", fmt.Errorf("backup de %s: %w", base, err)
	}
	return path, prune(dir, base, keep)
}

type backupFile struct {
	name  string
	stamp string
	n     int
}

// prune apaga as cópias de base mais antigas além de keep. Só considera nomes
// exatamente no formato das cópias, para não tocar nas de outro banco cujo
// nome comece igual.
func prune(dir, base string, keep int) error {
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(base) + `-(\d{8}T\d{6})(?:-(\d+))?\.db$`)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var files []backupFile
	for _, e := range entries {
		m := re.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		n := 0
		if m[2] != "" {
			n, _ = strconv.Atoi(m[2])
		}
		files = append(files, backupFile{e.Name(), m[1], n})
	}
	if len(files) <= keep {
		return nil
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].stamp != files[j].stamp {
			return files[i].stamp < files[j].stamp
		}
		return files[i].n < files[j].n
	})
	for _, f := range files[:len(files)-keep] {
		if err := os.Remove(filepath.Join(dir, f.name)); err != nil {
			return err
		}
	}
	return nil
}
