package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Item é uma unidade de trabalho (tarefa, missão ou decisão). O núcleo só
// guarda e lê; as regras de transição são do processo (internal/process).
type Item struct {
	ID            string         `json:"id"`
	Tipo          string         `json:"tipo"`
	Titulo        string         `json:"titulo"`
	Estado        string         `json:"estado"`
	Dono          string         `json:"dono"`
	Prioridade    string         `json:"prioridade"`
	Area          string         `json:"area"`
	Categoria     string         `json:"categoria"`
	Sprint        string         `json:"sprint"`
	Arquivos      []string       `json:"arquivos"`
	Recurso       string         `json:"recurso"`
	Folga         bool           `json:"folga"`
	Bloqueante    bool           `json:"bloqueante"`
	Visivel       bool           `json:"visivel_ao_usuario"`
	Bloqueios     int            `json:"bloqueios"`
	Ponta         string         `json:"ponta"`
	PontaRevisada string         `json:"ponta_revisada"`
	Commit        string         `json:"commit"`
	Marco         int            `json:"marco"`
	Campos        map[string]any `json:"campos"`
	Fonte         string         `json:"fonte"`
	CriadoEm      time.Time      `json:"criado_em"`
	AtualizadoEm  time.Time      `json:"atualizado_em"`
	EstadoDesde   time.Time      `json:"estado_desde"`
}

// Note é uma nota de um item (bloqueio, rejeição, validação, comentário).
type Note struct {
	ID     int64     `json:"id"`
	Item   string    `json:"item"`
	Tipo   string    `json:"tipo"`
	Texto  string    `json:"texto"`
	Vaga   string    `json:"vaga"`
	Quando time.Time `json:"quando"`
}

// ItemFilter escolhe itens; campos vazios não filtram.
type ItemFilter struct {
	IDs, Estados, Tipos []string
	Dono, Sprint        string
}

// ItemTx é o que uma regra do processo pode fazer dentro de uma transação de
// WithItems: ler e gravar itens, anotar, avisar por mensagem de sistema.
type ItemTx interface {
	Now() time.Time
	Get(id string) (Item, error)
	List(f ItemFilter) ([]Item, error)
	Agent(vaga string) (Agent, error)
	Agents() ([]Agent, error)
	Save(it Item, evento string, dados map[string]any) error
	Note(item, tipo, texto string) error
	Notes(item string) ([]Note, error)
	Notify(para, tipo, ref, corpo string) error
	// Sprint devolve a sprint aberta (nil se não há), lida na mesma transação.
	Sprint() (*Sprint, error)
	// Event grava um evento avulso (que não é mudança de item) como quem.
	Event(tipo, ref string, dados map[string]any) error
}

const itemCols = `id, tipo, titulo, estado, dono, prioridade, area, categoria, sprint, arquivos, recurso,
	folga, bloqueante, visivel, bloqueios, ponta, ponta_revisada, commit_, marco, campos, fonte,
	criado_em, atualizado_em, estado_desde`

func scanItem(row scanner) (Item, error) {
	var it Item
	var arquivos, campos, criado, atualizado, desde string
	err := row.Scan(&it.ID, &it.Tipo, &it.Titulo, &it.Estado, &it.Dono, &it.Prioridade, &it.Area, &it.Categoria,
		&it.Sprint, &arquivos, &it.Recurso, &it.Folga, &it.Bloqueante, &it.Visivel, &it.Bloqueios, &it.Ponta,
		&it.PontaRevisada, &it.Commit, &it.Marco, &campos, &it.Fonte, &criado, &atualizado, &desde)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, err
	}
	if err := json.Unmarshal([]byte(arquivos), &it.Arquivos); err != nil {
		return Item{}, fmt.Errorf("item %s: arquivos: %w", it.ID, err)
	}
	if err := json.Unmarshal([]byte(campos), &it.Campos); err != nil {
		return Item{}, fmt.Errorf("item %s: campos: %w", it.ID, err)
	}
	if it.Arquivos == nil {
		it.Arquivos = []string{}
	}
	if it.Campos == nil {
		it.Campos = map[string]any{}
	}
	it.CriadoEm, it.AtualizadoEm, it.EstadoDesde = parseTime(criado), parseTime(atualizado), parseTime(desde)
	return it, nil
}

func listItems(ctx context.Context, q querier, f ItemFilter) ([]Item, error) {
	var where []string
	var args []any
	in := func(col string, vals []string) {
		if len(vals) == 0 {
			return
		}
		where = append(where, col+" IN ("+strings.TrimSuffix(strings.Repeat("?,", len(vals)), ",")+")")
		for _, v := range vals {
			args = append(args, v)
		}
	}
	in("id", f.IDs)
	in("estado", f.Estados)
	in("tipo", f.Tipos)
	if f.Dono != "" {
		where = append(where, "dono = ?")
		args = append(args, f.Dono)
	}
	if f.Sprint != "" {
		where = append(where, "sprint = ?")
		args = append(args, f.Sprint)
	}
	query := `SELECT ` + itemCols + ` FROM itens`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	rows, err := q.QueryContext(ctx, query+" ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	its := []Item{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		its = append(its, it)
	}
	return its, rows.Err()
}

func listNotes(ctx context.Context, q querier, item string) ([]Note, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, item, tipo, texto, vaga, quando FROM itens_notas WHERE item = ? ORDER BY id`, item)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ns := []Note{}
	for rows.Next() {
		var n Note
		var quando string
		if err := rows.Scan(&n.ID, &n.Item, &n.Tipo, &n.Texto, &n.Vaga, &quando); err != nil {
			return nil, err
		}
		n.Quando = parseTime(quando)
		ns = append(ns, n)
	}
	return ns, rows.Err()
}

// Items lista itens pelo filtro, ordenados por id.
func (c *Core) Items(ctx context.Context, f ItemFilter) ([]Item, error) {
	return listItems(ctx, c.db, f)
}

// Item devolve o item e as notas dele.
func (c *Core) Item(ctx context.Context, id string) (Item, []Note, error) {
	it, err := scanItem(c.db.QueryRowContext(ctx, `SELECT `+itemCols+` FROM itens WHERE id = ?`, id))
	if err != nil {
		return Item{}, nil, fmt.Errorf("item %s: %w", id, err)
	}
	ns, err := listNotes(ctx, c.db, id)
	return it, ns, err
}

// WithItems roda fn numa transação do banco do projeto, como quem. Tudo o que
// fn grava (itens, notas, eventos e mensagens de sistema) entra junto ou não
// entra; quem foi avisado só acorda depois do commit.
//
// fn só pode usar o ItemTx: chamar métodos do Core dentro dela trava, porque o
// banco tem uma conexão só e a transação a está segurando.
func (c *Core) WithItems(ctx context.Context, quem string, fn func(ItemTx) error) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t := &itemTx{c: c, ctx: ctx, tx: tx, quem: quem, now: c.now().UTC()}
	if err := fn(t); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if t.notified {
		c.notify.Broadcast()
	}
	return nil
}

type itemTx struct {
	c        *Core
	ctx      context.Context
	tx       *sql.Tx
	quem     string
	now      time.Time
	notified bool
}

func (t *itemTx) stamp() string  { return t.now.Format(TimeLayout) }
func (t *itemTx) Now() time.Time { return t.now }

func (t *itemTx) Get(id string) (Item, error) {
	it, err := scanItem(t.tx.QueryRowContext(t.ctx, `SELECT `+itemCols+` FROM itens WHERE id = ?`, id))
	if err != nil {
		return Item{}, fmt.Errorf("item %s: %w", id, err)
	}
	return it, nil
}

func (t *itemTx) List(f ItemFilter) ([]Item, error) { return listItems(t.ctx, t.tx, f) }

func (t *itemTx) Agent(vaga string) (Agent, error) {
	a, err := scanAgent(t.tx.QueryRowContext(t.ctx, agentSelect+` WHERE vaga = ?`, vaga))
	if err != nil {
		return Agent{}, fmt.Errorf("vaga %s: %w", vaga, err)
	}
	t.c.deriveState(&a, t.now)
	return a, nil
}

func (t *itemTx) Agents() ([]Agent, error) {
	rows, err := t.tx.QueryContext(t.ctx, agentSelect+` ORDER BY vaga`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	as := []Agent{}
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		t.c.deriveState(&a, t.now)
		as = append(as, a)
	}
	return as, rows.Err()
}

// Save insere ou atualiza o item e grava o evento item.<evento> (ref = id).
// estado_desde muda só quando o estado muda.
func (t *itemTx) Save(it Item, evento string, dados map[string]any) error {
	if it.ID == "" || it.Tipo == "" || it.Estado == "" {
		return invalid("item precisa de id, tipo e estado")
	}
	if evento == "" {
		return invalid("evento do item vazio")
	}
	if it.Arquivos == nil {
		it.Arquivos = []string{}
	}
	if it.Campos == nil {
		it.Campos = map[string]any{}
	}
	arquivos, err := json.Marshal(it.Arquivos)
	if err != nil {
		return err
	}
	campos, err := json.Marshal(it.Campos)
	if err != nil {
		return invalid("campos do item não viram JSON: %v", err)
	}
	stamp := t.stamp()
	antes, err := t.Get(it.ID)
	novo := errors.Is(err, ErrNotFound)
	if err != nil && !novo {
		return err
	}
	criado, desde := stamp, stamp
	if !novo {
		criado = antes.CriadoEm.UTC().Format(TimeLayout)
		if antes.Estado == it.Estado {
			desde = antes.EstadoDesde.UTC().Format(TimeLayout)
		}
	}
	if _, err := t.tx.ExecContext(t.ctx, `INSERT INTO itens (`+itemCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (id) DO UPDATE SET tipo = excluded.tipo, titulo = excluded.titulo, estado = excluded.estado,
		dono = excluded.dono, prioridade = excluded.prioridade, area = excluded.area, categoria = excluded.categoria,
		sprint = excluded.sprint, arquivos = excluded.arquivos, recurso = excluded.recurso, folga = excluded.folga,
		bloqueante = excluded.bloqueante, visivel = excluded.visivel, bloqueios = excluded.bloqueios,
		ponta = excluded.ponta, ponta_revisada = excluded.ponta_revisada, commit_ = excluded.commit_,
		marco = excluded.marco, campos = excluded.campos, fonte = excluded.fonte,
		atualizado_em = excluded.atualizado_em, estado_desde = excluded.estado_desde`,
		it.ID, it.Tipo, it.Titulo, it.Estado, it.Dono, it.Prioridade, it.Area, it.Categoria, it.Sprint, string(arquivos),
		it.Recurso, it.Folga, it.Bloqueante, it.Visivel, it.Bloqueios, it.Ponta, it.PontaRevisada, it.Commit, it.Marco,
		string(campos), it.Fonte, criado, stamp, desde); err != nil {
		return err
	}
	d := map[string]any{"estado": it.Estado}
	if !novo && antes.Estado != it.Estado {
		d["de"] = antes.Estado
	}
	for k, v := range dados {
		d[k] = v
	}
	return event(t.ctx, t.tx, stamp, t.quem, "item."+evento, it.ID, d)
}

func (t *itemTx) Note(item, tipo, texto string) error {
	if tipo == "" || texto == "" {
		return invalid("nota precisa de tipo e texto")
	}
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO itens_notas (item, tipo, texto, vaga, quando) VALUES (?, ?, ?, ?, ?)`,
		item, tipo, texto, t.quem, t.stamp())
	return err
}

func (t *itemTx) Notes(item string) ([]Note, error) { return listNotes(t.ctx, t.tx, item) }

func (t *itemTx) Sprint() (*Sprint, error) { return currentSprint(t.ctx, t.tx) }

func (t *itemTx) Event(tipo, ref string, dados map[string]any) error {
	if dados == nil {
		dados = map[string]any{}
	}
	return event(t.ctx, t.tx, t.stamp(), t.quem, tipo, ref, dados)
}

// LastEvent devolve a hora do evento mais recente do tipo (ok=false se nunca houve).
func (c *Core) LastEvent(ctx context.Context, tipo string) (time.Time, bool, error) {
	var quando string
	err := c.db.QueryRowContext(ctx, `SELECT quando FROM eventos WHERE tipo = ? ORDER BY seq DESC LIMIT 1`, tipo).Scan(&quando)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return parseTime(quando), true, nil
}

func (t *itemTx) Notify(para, tipo, ref, corpo string) error {
	if err := t.c.sendSystem(t.ctx, t.tx, para, tipo, ref, corpo); err != nil {
		return err
	}
	t.notified = true
	return nil
}
