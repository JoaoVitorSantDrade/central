// Package store abre bancos SQLite com as configurações da Central e aplica migrações.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// Options ajusta a abertura de um banco.
type Options struct {
	// BackupDir recebe uma cópia do banco antes de migrar um banco existente
	// (vazio: sem cópia).
	BackupDir string
}

// Open abre (criando se preciso) o banco em path, em modo WAL, e aplica as
// migrações cujo índice ainda não foi aplicado. Cada migração roda numa transação.
// Um banco existente que vai migrar é copiado antes para opt.BackupDir.
func Open(path string, migrations []string, opt Options) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// _txlock=immediate: toda transação pega a trava de escrita já no BEGIN. Sem isso,
	// duas transações que leem e depois escrevem (mesmo de dois *sql.DB no mesmo
	// arquivo) falham com SQLITE_BUSY em vez de esperar; a entrega exatamente uma
	// vez (ler o cursor e avançá-lo na mesma transação) depende disso.
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Um dono do banco: o daemon. Uma conexão evita disputa de escrita.
	db.SetMaxOpenConns(1)
	before := func() error {
		if opt.BackupDir == "" {
			return nil
		}
		base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		_, err := Backup(db, opt.BackupDir, base, KeepBackups)
		return err
	}
	if err := migrate(db, migrations, before); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrar %s: %w", path, err)
	}
	return db, nil
}

// migrate aplica as migrações pendentes. before roda uma vez antes da primeira
// delas, só num banco que já tinha alguma migração aplicada.
func migrate(db *sql.DB, migrations []string, before func() error) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (versao INTEGER NOT NULL)`); err != nil {
		return err
	}
	var v int
	err := db.QueryRow(`SELECT versao FROM schema_version`).Scan(&v)
	if err == sql.ErrNoRows {
		if _, err := db.Exec(`INSERT INTO schema_version (versao) VALUES (0)`); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if v > len(migrations) {
		return fmt.Errorf("banco na versão %d é mais novo que este binário (%d)", v, len(migrations))
	}
	if v > 0 && v < len(migrations) {
		if err := before(); err != nil {
			return fmt.Errorf("backup antes de migrar: %w", err)
		}
	}
	for i := v; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migração %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`UPDATE schema_version SET versao = ?`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
