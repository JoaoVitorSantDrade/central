package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Lock é uma trava nomeada. Não tem prazo próprio: vale enquanto o dono não
// estiver sumido nem encerrado (Decisão 3 do plano). DonoEstado é o estado
// visível do dono na hora da leitura.
type Lock struct {
	Nome       string    `json:"nome"`
	Vaga       string    `json:"vaga"`
	Motivo     string    `json:"motivo"`
	Inicio     time.Time `json:"inicio"`
	DonoEstado string    `json:"dono_estado"`
}

var nomeTrava = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,128}$`)

const lockSelect = `SELECT t.nome, t.vaga, t.motivo, t.inicio, ` + agentCols + ` FROM travas t JOIN agentes a ON a.vaga = t.vaga`

// scanLock lê uma linha de lockSelect e preenche DonoEstado com o estado visível.
func (c *Core) scanLock(row scanner, now time.Time, s Settings) (Lock, error) {
	var l Lock
	var inicio string
	var a Agent
	var raw agentRaw
	err := row.Scan(append([]any{&l.Nome, &l.Vaga, &l.Motivo, &inicio}, raw.dest(&a)...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return Lock{}, ErrNotFound
	}
	if err != nil {
		return Lock{}, err
	}
	l.Inicio = parseTime(inicio)
	raw.fill(&a)
	l.DonoEstado = a.Estado
	if c.sumido(a, now, s) {
		l.DonoEstado = EstadoSumido
	}
	return l, nil
}

// vivo diz se o dono de uma trava ainda a segura.
func vivo(l Lock) bool { return l.DonoEstado != EstadoSumido && l.DonoEstado != EstadoEncerrado }

// Lock toma a trava nome para a vaga. Livre, ou com o dono sumido ou encerrado,
// a vaga toma (evento trava.tomada, com de = dono anterior se havia); da própria
// vaga, devolve a trava como está; de outra vaga viva, ErrConflict.
func (c *Core) Lock(ctx context.Context, vaga, nome, motivo string) (Lock, error) {
	if !nomeTrava.MatchString(nome) {
		return Lock{}, invalid("nome de trava %q (use até 128 letras, dígitos, ., _, / ou -)", nome)
	}
	if motivo == "" {
		return Lock{}, invalid("o motivo da trava é obrigatório")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return Lock{}, err
	}
	defer tx.Rollback()
	dono, err := scanAgent(tx.QueryRowContext(ctx, agentSelect+` WHERE vaga = ?`, vaga))
	if err != nil {
		return Lock{}, fmt.Errorf("vaga %q: %w", vaga, err)
	}
	// Uma trava de vaga encerrada nasceria morta: ninguém a respeitaria.
	if dono.Estado == EstadoEncerrado {
		return Lock{}, fmt.Errorf("%w: a vaga %s está encerrada (entre de novo para voltar a travar)", ErrConflict, vaga)
	}
	now, s := c.now(), c.currentSettings()
	atual, err := c.scanLock(tx.QueryRowContext(ctx, lockSelect+` WHERE t.nome = ?`, nome), now, s)
	switch {
	case errors.Is(err, ErrNotFound):
	case err != nil:
		return Lock{}, err
	case atual.Vaga == vaga:
		return atual, nil
	case vivo(atual):
		return Lock{}, fmt.Errorf("%w: a trava %q é de %s (%s): %s", ErrConflict, nome, atual.Vaga, atual.DonoEstado, atual.Motivo)
	}
	stamp := now.UTC().Format(TimeLayout)
	if _, err := tx.ExecContext(ctx, `INSERT INTO travas (nome, vaga, motivo, inicio) VALUES (?, ?, ?, ?)
		ON CONFLICT (nome) DO UPDATE SET vaga = excluded.vaga, motivo = excluded.motivo, inicio = excluded.inicio`,
		nome, vaga, motivo, stamp); err != nil {
		return Lock{}, err
	}
	dados := map[string]string{"motivo": motivo}
	if atual.Vaga != "" {
		dados["de"] = atual.Vaga
	}
	if err := event(ctx, tx, stamp, vaga, "trava.tomada", nome, dados); err != nil {
		return Lock{}, err
	}
	l, err := c.scanLock(tx.QueryRowContext(ctx, lockSelect+` WHERE t.nome = ?`, nome), now, s)
	if err != nil {
		return Lock{}, err
	}
	return l, tx.Commit()
}

// Unlock solta a trava nome da vaga. Trava inexistente: ErrNotFound; de outra
// vaga: ErrConflict.
func (c *Core) Unlock(ctx context.Context, vaga, nome string) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var dono string
	err = tx.QueryRowContext(ctx, `SELECT vaga FROM travas WHERE nome = ?`, nome).Scan(&dono)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("trava %q: %w", nome, ErrNotFound)
	}
	if err != nil {
		return err
	}
	if dono != vaga {
		return fmt.Errorf("%w: a trava %q é de %s, não de %s", ErrConflict, nome, dono, vaga)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM travas WHERE nome = ?`, nome); err != nil {
		return err
	}
	if err := event(ctx, tx, c.stamp(), vaga, "trava.solta", nome, map[string]string{}); err != nil {
		return err
	}
	return tx.Commit()
}

// Locks lista as travas em ordem de nome, com o estado visível do dono.
func (c *Core) Locks(ctx context.Context) ([]Lock, error) {
	rows, err := c.db.QueryContext(ctx, lockSelect+` ORDER BY t.nome`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now, s := c.now(), c.currentSettings()
	ls := []Lock{}
	for rows.Next() {
		l, err := c.scanLock(rows, now, s)
		if err != nil {
			return nil, err
		}
		ls = append(ls, l)
	}
	return ls, rows.Err()
}

// releaseLocks solta, dentro de tx, todas as travas da vaga e devolve os nomes.
func releaseLocks(ctx context.Context, tx *sql.Tx, vaga string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `DELETE FROM travas WHERE vaga = ? RETURNING nome`, vaga)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nomes := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		nomes = append(nomes, n)
	}
	return nomes, rows.Err()
}
