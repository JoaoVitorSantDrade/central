package store

import (
	"path/filepath"
	"testing"
)

func TestBackupMantemUltimos(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "x.db"), migs, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bdir := filepath.Join(dir, "backups")
	var ultimo string
	for i := 1; i <= 3; i++ {
		if _, err := db.Exec(`INSERT INTO a (x) VALUES (?)`, i); err != nil {
			t.Fatal(err)
		}
		p, err := Backup(db, bdir, "x", 2)
		if err != nil {
			t.Fatalf("backup %d: %v", i, err)
		}
		ultimo = p
	}
	files, err := filepath.Glob(filepath.Join(bdir, "x-*.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("backups = %v, quer 2", files)
	}
	b, err := Open(ultimo, migs, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var n int
	if err := b.QueryRow(`SELECT count(*) FROM a`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("linhas no backup mais novo = %d, %v", n, err)
	}
}

func TestBackupAntesDeMigrar(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.db")
	bdir := filepath.Join(dir, "backups")
	db, err := Open(p, migs[:1], Options{BackupDir: bdir})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	// banco novo: nada a salvar
	if files, _ := filepath.Glob(filepath.Join(bdir, "*.db")); len(files) != 0 {
		t.Fatalf("backups de banco novo = %v", files)
	}
	db, err = Open(p, migs, Options{BackupDir: bdir})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	files, _ := filepath.Glob(filepath.Join(bdir, "x-*.db"))
	if len(files) != 1 {
		t.Fatalf("backups = %v, quer 1", files)
	}
	// já na versão atual: nada a salvar
	db, err = Open(p, migs, Options{BackupDir: bdir})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if files, _ := filepath.Glob(filepath.Join(bdir, "x-*.db")); len(files) != 1 {
		t.Fatalf("backups = %v, quer 1", files)
	}
}
