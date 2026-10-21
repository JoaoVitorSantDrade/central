package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var migs = []string{
	`CREATE TABLE a (x INTEGER)`,
	`CREATE TABLE b (y INTEGER)`,
}

func TestOpenAplicaMigracoesUmaVez(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "x.db")
	for i := 0; i < 2; i++ {
		db, err := Open(p, migs, Options{})
		if err != nil {
			t.Fatalf("abertura %d: %v", i, err)
		}
		var v int
		if err := db.QueryRow(`SELECT versao FROM schema_version`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		if v != len(migs) {
			t.Fatalf("schema_version = %d, quer %d", v, len(migs))
		}
		var n int
		db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('a','b')`).Scan(&n)
		if n != 2 {
			t.Fatalf("tabelas = %d", n)
		}
		db.Close()
	}
	// uma migração nova é aplicada sozinha
	db, err := Open(p, append(migs, `CREATE TABLE c (z INTEGER)`), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v int
	db.QueryRow(`SELECT versao FROM schema_version`).Scan(&v)
	if v != 3 {
		t.Fatalf("schema_version = %d, quer 3", v)
	}
}

func TestOpenUsaWAL(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "x.db"), migs, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var mode string
	db.QueryRow(`PRAGMA journal_mode`).Scan(&mode)
	if mode != "wal" {
		t.Fatalf("journal_mode = %q", mode)
	}
}

// Duas conexões (dois *sql.DB) que leem e depois escrevem na mesma transação:
// com BEGIN DEFERRED a segunda escrita falha com SQLITE_BUSY (leitura velha);
// com _txlock=immediate a segunda transação espera a primeira terminar.
func TestTxImediata(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.db")
	mig := []string{`CREATE TABLE c (n INTEGER NOT NULL); INSERT INTO c (n) VALUES (0);`}
	dbs := make([]*sql.DB, 2)
	for i := range dbs {
		db, err := Open(p, mig, Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		dbs[i] = db
	}
	var wg sync.WaitGroup
	leu := []chan struct{}{make(chan struct{}), make(chan struct{})}
	errs := make(chan error, 2)
	for i, db := range dbs {
		wg.Add(1)
		go func(i int, db *sql.DB) {
			defer wg.Done()
			tx, err := db.BeginTx(context.Background(), nil)
			if err != nil {
				errs <- err
				return
			}
			defer tx.Rollback()
			var n int
			if err := tx.QueryRow(`SELECT n FROM c`).Scan(&n); err != nil {
				errs <- err
				return
			}
			close(leu[i])
			// Espera a outra ler também (sem imediata) ou desiste (com imediata ela está bloqueada).
			select {
			case <-leu[1-i]:
			case <-time.After(300 * time.Millisecond):
			}
			if _, err := tx.Exec(`UPDATE c SET n = ?`, n+1); err != nil {
				errs <- err
				return
			}
			errs <- tx.Commit()
		}(i, db)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("transação: %v", err)
		}
	}
	var n int
	if err := dbs[0].QueryRow(`SELECT n FROM c`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("n = %d, quer 2 (uma atualização se perdeu)", n)
	}
}
