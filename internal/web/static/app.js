// Painel da Central (spec §8). Só lê pela API /v1 e só escreve mensagem do
// humano, veto e resposta de decisão. Sem dependências: roda offline.
'use strict';

const ABAS = [
  ['agora', 'Agora'],
  ['quadro', 'Quadro'],
  ['conversa', 'Conversa'],
  ['linha', 'Linha do tempo'],
  ['sprint', 'Sprint'],
  ['decisoes', 'Decisões'],
];
const COLUNAS = [
  ['proposta', 'Proposta'],
  ['pronta', 'Pronta'],
  ['em_andamento', 'Em andamento'],
  ['concluida', 'Concluída'],
  ['em_revisao', 'Em revisão'],
  ['aprovada', 'Aprovada'],
  ['integrada', 'Integrada'],
  ['validada', 'Validada'],
];
// Estados que caem numa coluna vizinha, com selo próprio.
const COLUNA_DE = { bloqueada: 'em_andamento', rejeitada: 'em_andamento' };
const POR_COLUNA = 30;
const CORES_ESTADO = {
  ativo: 'var(--acento)', trabalhando: 'var(--ok)', ocioso: 'var(--alerta)',
  sumido: 'var(--perigo)', encerrado: 'var(--neutro-suave)', suspenso: 'var(--perigo)',
};
const FINAIS_DECISAO = ['aprovada', 'vetada'];
const REFRESCO_MIN_MS = 1000;
const REFRESCO_MAQUINA_MS = 5000;
const REFRESCO_RELOGIO_MS = 30000;

const estado = {
  rota: {},
  projeto: null, // id do projeto aberto
  fonte: null, // EventSource do projeto aberto
  timer: null,
  ultimoRefresco: 0,
  geracao: 0,
  filtro: { quem: '', item: '', semSistema: false },
  janela: '6h',
  colunaAberta: {},
  exts: {}, // id do projeto → extensões declaradas
  extDados: {}, // projeto/extensão → última resposta da ação da aba
};

// ---------- utilidades ----------

const $ = (s, raiz = document) => raiz.querySelector(s);
const esc = (v) => String(v ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const enc = encodeURIComponent;

async function api(caminho, opcoes = {}) {
  const init = { method: opcoes.method || 'GET', headers: {} };
  if (opcoes.body !== undefined) {
    init.headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(opcoes.body);
  }
  const r = await fetch('/v1' + caminho, init);
  let j;
  try { j = await r.json(); } catch { throw new Error(`HTTP ${r.status}`); }
  if (!j.ok) throw new Error(j.erro?.mensagem || `HTTP ${r.status}`);
  return j;
}

function data(t) {
  if (!t) return null;
  const d = new Date(t);
  return isNaN(d) || d.getFullYear() < 2000 ? null : d;
}
function hora(t) {
  const d = data(t);
  return d ? d.toLocaleTimeString('pt-BR', { hour: '2-digit', minute: '2-digit' }) : '—';
}
function duracao(ms) {
  const min = Math.round(ms / 60000);
  if (min < 1) return 'menos de 1 min';
  if (min < 60) return `${min} min`;
  const h = Math.floor(min / 60);
  if (h < 48) return `${h} h${min % 60 ? ` ${min % 60} min` : ''}`;
  return `${Math.floor(h / 24)} d`;
}
function ha(t) {
  const d = data(t);
  return d ? `há ${duracao(Date.now() - d)}` : '—';
}
function selo(texto, classe = '') {
  return `<span class="selo ${classe}">${esc(texto)}</span>`;
}
function seloEstado(e) {
  return selo(e.replace('_', ' '), `s-${e}`);
}
function num(v, casas = 1) {
  return Number.isFinite(v) ? v.toLocaleString('pt-BR', { maximumFractionDigits: casas }) : '—';
}

// ---------- roteamento ----------

function lerRota() {
  const partes = location.hash.replace(/^#\/?/, '').split('/').filter(Boolean).map(decodeURIComponent);
  if (partes[0] === 'p' && partes[1]) {
    if (partes[2] === 'ext' && partes[3]) return { id: partes[1], aba: 'ext', ext: partes[3] };
    const aba = ABAS.some(([a]) => a === partes[2]) ? partes[2] : 'agora';
    return { id: partes[1], aba };
  }
  return {};
}

function aoMudarRota() {
  estado.rota = lerRota();
  estado.geracao++;
  ligarStream(estado.rota.id || null);
  desenharMoldura();
  refrescar(true);
}

function desenharMoldura() {
  const { id, aba } = estado.rota;
  const trilha = $('#trilha');
  const abas = $('#abas');
  if (!id) {
    trilha.innerHTML = '<span>Projetos desta máquina</span>';
    abas.hidden = true;
  } else {
    trilha.innerHTML = `<a href="#/">Projetos</a> / <b>${esc(id)}</b>`;
    abas.hidden = false;
    abas.innerHTML = ABAS.map(([a, nome]) =>
      `<a href="#/p/${enc(id)}/${a}" class="${a === aba ? 'atual' : ''}" data-aba="${a}">${esc(nome)}<span class="contagem"></span></a>`).join('') +
      '<span id="abas-ext"></span>';
    desenharAbasExt(id);
  }
  $('#app').innerHTML = '<div id="erro"></div><div id="fixo"></div><div id="dados"><p class="vazio">Carregando…</p></div>';
  if (id && aba === 'conversa') montarConversa(id);
  if (id && aba === 'linha') montarLinha();
  if (id && aba === 'ext') montarExt(id, estado.rota.ext);
}

// desenharAbasExt acrescenta uma aba por extensão com aba no central.toml.
async function desenharAbasExt(id) {
  try {
    if (!estado.exts[id]) estado.exts[id] = (await api(`/projetos/${enc(id)}/extensoes`)).extensoes;
  } catch { return; }
  const alvo = $('#abas-ext');
  if (!alvo || estado.rota.id !== id) return;
  alvo.style.display = 'contents';
  alvo.innerHTML = estado.exts[id].filter((x) => x.aba).map((x) =>
    `<a href="#/p/${enc(id)}/ext/${enc(x.nome)}" class="${estado.rota.ext === x.nome ? 'atual' : ''}">${esc(x.aba.titulo)}<span class="contagem" data-ext="${esc(x.nome)}"></span></a>`).join('');
}

// ---------- ao vivo ----------

async function ligarStream(id) {
  if (estado.projeto === id) return;
  if (estado.fonte) estado.fonte.close();
  estado.fonte = null;
  estado.projeto = id;
  const vivo = $('#vivo');
  vivo.className = 'vivo';
  if (!id) return;
  let ultimo = 0;
  try { ultimo = (await api(`/projetos/${enc(id)}/eventos?tipos=_`)).ultimo; } catch { /* o stream começa do zero */ }
  if (estado.projeto !== id) return;
  const fonte = new EventSource(`/v1/projetos/${enc(id)}/stream?depois=${ultimo}`);
  fonte.onopen = () => { vivo.className = 'vivo on'; vivo.title = 'Ao vivo'; };
  fonte.onerror = () => { vivo.className = 'vivo off'; vivo.title = 'Reconectando…'; };
  fonte.onmessage = () => agendar();
  estado.fonte = fonte;
}

// agendar pede um refresco com no máximo um por segundo.
function agendar() {
  if (estado.timer) return;
  const espera = Math.max(0, REFRESCO_MIN_MS - (Date.now() - estado.ultimoRefresco));
  estado.timer = setTimeout(() => { estado.timer = null; refrescar(false); }, espera);
}

// ocupado diz se o humano está digitando em algo que o refresco apagaria.
function ocupado() {
  const dados = $('#dados');
  if (!dados) return false;
  // O formulário da conversa fica fora do que o refresco redesenha.
  return [...dados.querySelectorAll('textarea, input[type=text]')]
    .filter((el) => !el.closest('[data-livre]'))
    .some((el) => el === document.activeElement || el.value.trim() !== '');
}

async function refrescar(forcar) {
  if (!forcar && ocupado()) { setTimeout(agendar, REFRESCO_MIN_MS); return; }
  estado.ultimoRefresco = Date.now();
  const geracao = estado.geracao;
  const { id, aba } = estado.rota;
  try {
    const html = id ? await VISTAS[aba](id, forcar) : await vistaMaquina();
    if (geracao !== estado.geracao) return;
    if (html !== undefined) $('#dados').innerHTML = html;
    $('#erro').innerHTML = '';
    if (id) contarDecisoes(id);
  } catch (e) {
    if (geracao !== estado.geracao) return;
    $('#erro').innerHTML = `<div class="erro">${esc(e.message)}</div>`;
  }
}

async function contarDecisoes(id) {
  try {
    const { itens } = await api(`/projetos/${enc(id)}/itens?tipo=decisao&estado=humano`);
    const alvo = $('#abas [data-aba=decisoes] .contagem');
    if (alvo) alvo.innerHTML = itens.length ? selo(itens.length, 's-alerta sem-ponto') : '';
  } catch { /* só o selo */ }
}

// ---------- máquina ----------

async function vistaMaquina() {
  const { projetos } = await api('/projetos');
  if (!projetos.length) {
    return '<p class="vazio">Nenhum projeto ainda. Um agente entra com <code>central entrar</code> e o projeto aparece aqui.</p>';
  }
  return `<div class="grade">${projetos.map(cartaoProjeto).join('')}</div>`;
}

function cartaoProjeto(p) {
  const vagas = Object.entries(p.vagas || {}).filter(([e]) => e !== 'encerrado')
    .sort(([a], [b]) => a.localeCompare(b)).map(([e, n]) => selo(`${n} ${e}`, `s-${e}`)).join(' ');
  let sprint = '<span class="fraco">sem sprint aberta</span>';
  if (p.sprint) {
    const prox = proximaCerimonia(p.sprint);
    sprint = `<span>${esc(p.sprint.id)}</span> <span class="fraco">· ${prox ? `${esc(prox[0])} às ${hora(prox[1])}` : `fim às ${hora(p.sprint.fim)}`}</span>`;
  }
  return `<a class="cartao" href="#/p/${enc(p.id)}/agora">
    <div class="linha entre"><h2>${esc(p.nome || p.id)}</h2><span class="fraco">${ha(p.ultimo_acesso)}</span></div>
    <div class="fraco mono">${esc(p.id)}</div>
    <div class="fraco mono corta" title="${esc(p.raiz)}">&lrm;${esc(p.raiz)}&lrm;</div>
    <div class="numeros">
      <div class="numero"><b>${p.fila_qa}</b><span>fila de QA</span></div>
      <div class="numero ${p.alertas ? 'ruim' : ''}"><b>${p.alertas}</b><span>alertas</span></div>
    </div>
    <div class="linha" style="margin:6px 0">${vagas || '<span class="fraco">nenhuma vaga ativa</span>'}</div>
    <div class="linha">${sprint}</div>
    ${p.erro ? `<div class="erro" style="margin:8px 0 0">${esc(p.erro)}</div>` : ''}
  </a>`;
}

function proximaCerimonia(s) {
  const agora = Date.now();
  for (const nome of ['abertura', 'checkin', 'escopo', 'congelamento', 'corte', 'retro', 'fim']) {
    const d = data(s[nome]);
    if (d && d > agora) return [nome, s[nome]];
  }
  return null;
}

// ---------- agora ----------

async function vistaAgora(id) {
  const p = `/projetos/${enc(id)}`;
  const [{ agentes }, { recursos }, { itens }, saude, exec] = await Promise.all([
    api(`${p}/agentes`), api(`${p}/recursos`), api(`${p}/itens?estado=em_andamento,bloqueada`), api(`${p}/saude`).then((r) => r.saude),
    api(`${p}/vagas`),
  ]);
  // Vagas que a Central executa: as que ainda não entraram também aparecem.
  const execDe = Object.fromEntries(exec.vagas.map((v) => [v.vaga, v]));
  for (const v of exec.vagas) {
    if (!agentes.some((a) => a.vaga === v.vaga)) {
      agentes.push({ vaga: v.vaga, papel: v.papel, area: v.area, status: '', batimento: null, executada: true, estado: estadoExecutada(v) });
    }
  }
  const temExec = exec.vagas.length > 0;
  const reservas = {};
  for (const it of itens) (reservas[it.dono] ||= []).push(it);
  const ordem = (a) => (a.estado === 'encerrado' ? 1 : 0);
  agentes.sort((a, b) => ordem(a) - ordem(b) || a.vaga.localeCompare(b.vaga));
  const alertas = saude.alertas.length
    ? `<div class="cartao secao"><h3>Atenção</h3>${saude.alertas.map((a) => `<div>${selo('alerta', 's-alerta')} ${esc(a)}</div>`).join('')}</div>`
    : '';
  const linhas = agentes.map((a) => `<tr class="${a.estado === 'sumido' ? 'ruim' : ''}" ${a.estado === 'encerrado' ? 'style="opacity:.55"' : ''}>
      <td><b>${esc(a.vaga)}</b>${a.area ? `<div class="fraco">${esc(a.area)}</div>` : ''}</td>
      <td>${esc(a.papel)}</td>
      <td>${seloEstado(a.estado)}</td>
      <td>${esc(a.status) || '<span class="fraco">—</span>'}</td>
      <td>${(reservas[a.vaga] || []).map((it) => `<div><code>${esc(it.id)}</code> ${it.estado === 'bloqueada' ? selo('bloqueada', 's-perigo') : ''} <span class="suave">${esc(it.titulo)}</span></div>`).join('') || '<span class="fraco">—</span>'}</td>
      ${temExec ? `<td>${execDe[a.vaga] ? celulaExecucao(execDe[a.vaga]) : '<span class="fraco">—</span>'}</td>` : ''}
      <td class="esconde-estreito" title="${esc(a.batimento)}">${ha(a.batimento)}</td>
    </tr>`).join('');
  const vagas = agentes.length
    ? `<div class="cartao secao tabela"><table>
        <thead><tr><th>Vaga</th><th>Papel</th><th>Estado</th><th>Status</th><th>Reserva</th>${temExec ? '<th>Execução</th>' : ''}<th class="esconde-estreito">Batimento</th></tr></thead>
        <tbody>${linhas}</tbody></table></div>`
    : '<p class="vazio">Nenhuma vaga entrou ainda.</p>';
  const nomes = Object.keys(recursos).sort();
  const rec = nomes.length ? `<h2>Recursos</h2><div class="grade">${nomes.map((n) => cartaoRecurso(n, recursos[n])).join('')}</div>` : '';
  const pausa = exec.plano_pausado_ate
    ? `<div class="faixa-pausa">Execução pausada até ${hora(exec.plano_pausado_ate)}: ${esc(exec.plano_motivo || 'limite do plano')}</div>`
    : '';
  const custo = temExec ? ` <span class="fraco">· US$ ${num(exec.custo_hoje, 2)} hoje</span>` : '';
  return pausa + alertas + `<h2>Vagas${custo}</h2>` + vagas + rec;
}

// estadoExecutada é o estado visível de uma vaga executada que ainda não entrou.
function estadoExecutada(v) {
  if (v.turno_desde) return 'trabalhando';
  return { aberta: 'ocioso', suspensa: 'suspenso', fechada: 'encerrado' }[v.estado] || v.estado;
}

function celulaExecucao(v) {
  const turno = v.turno_desde
    ? `turno desde ${hora(v.turno_desde)}`
    : v.estado === 'aberta' ? 'aguardando' : esc(v.estado);
  const falhas = v.falhas ? ` · ${selo(`${v.falhas} falha${v.falhas > 1 ? 's' : ''}`, 's-alerta sem-ponto')}` : '';
  const negacao = v.ultima_negacao ? `<div class="negacao" title="última permissão negada">${esc(v.ultima_negacao)}</div>` : '';
  const botoes = [];
  if (v.estado === 'aberta') botoes.push(`<button class="botao" data-vaga-op="fechar" data-vaga="${esc(v.vaga)}" data-papel="${esc(v.papel)}">Fechar</button>`);
  else botoes.push(`<button class="botao" data-vaga-op="reabrir" data-vaga="${esc(v.vaga)}" data-papel="${esc(v.papel)}">Reabrir</button>`);
  if (v.pasta && !v.turno_desde) botoes.push(`<button class="botao" data-vaga-op="app" data-vaga="${esc(v.vaga)}">Abrir no app</button>`);
  return `<div>${turno}${falhas}</div>
    <div class="fraco">${v.n_turnos} turno${v.n_turnos === 1 ? '' : 's'} · US$ ${num(v.custo_dia, 2)} hoje</div>${negacao}
    <div class="linha botoes-vaga">${botoes.join('')}<span class="fraco" data-status></span></div>`;
}

async function agirVaga(botao) {
  const { vagaOp: op, vaga, papel } = botao.dataset;
  const p = `/projetos/${enc(estado.rota.id)}`;
  if (op === 'fechar' && !confirm(`Fechar a vaga ${vaga}? O turno em curso termina e nenhum novo começa.`)) return;
  const status = botao.parentElement.querySelector('[data-status]');
  botao.disabled = true;
  try {
    if (op === 'fechar') await api(`${p}/vagas/${enc(vaga)}/fechar`, { method: 'POST', body: {} });
    else if (op === 'reabrir') await api(`${p}/vagas`, { method: 'POST', body: { vaga, papel } });
    else await api(`${p}/vagas/${enc(vaga)}/app`, { method: 'POST', body: {} });
    refrescar(true);
  } catch (e) {
    status.textContent = e.message;
    botao.disabled = false;
  }
}

function cartaoRecurso(nome, v) {
  const dims = Object.keys(v.spec.dimensoes || {}).sort();
  const barras = dims.map((d) => {
    const cap = v.spec.dimensoes[d] || 1;
    const dec = v.ativas.reduce((s, g) => s + (g.declarado?.[d] || 0), 0) + (v.spec.reservado?.[d] || 0);
    const med = v.ultima?.valores?.[d];
    const pct = (x) => Math.max(0, Math.min(100, (x / cap) * 100));
    return `<div style="margin:6px 0">
      <div class="linha entre"><span class="mono">${esc(d)}</span>
        <span class="fraco">medido ${med === undefined ? '—' : num(med, 0)} · declarado ${num(dec, 0)} / ${num(cap, 0)}</span></div>
      <div class="barra" title="roxo: declarado + reservado; azul: medido">
        <i class="dec" style="width:${pct(dec)}%"></i>${med === undefined ? '' : `<i style="width:${pct(med)}%"></i>`}
      </div></div>`;
  }).join('');
  const concessao = (g) => `<div><code>#${g.id}</code> <b>${esc(g.vaga)}</b> ${g.exclusivo ? selo('exclusivo', 's-roxo sem-ponto') : ''}
    <span class="fraco">${esc(g.prioridade)}${g.prazo ? ` · até ${hora(g.prazo)}` : ''}${g.motivo ? ` · ${esc(g.motivo)}` : ''}</span></div>`;
  return `<div class="cartao">
    <div class="linha entre"><h2>${esc(nome)}</h2><span class="fraco">${v.ultima ? `leitura ${ha(v.ultima.quando)}` : 'sem medidor'}</span></div>
    ${barras}
    <h3 style="margin-top:12px">Ativas (${v.ativas.length})</h3>${v.ativas.map(concessao).join('') || '<div class="fraco">nenhuma</div>'}
    <h3 style="margin-top:10px">Fila (${v.fila.length})</h3>${v.fila.map(concessao).join('') || '<div class="fraco">vazia</div>'}
  </div>`;
}

// ---------- quadro ----------

async function vistaQuadro(id) {
  const p = `/projetos/${enc(id)}`;
  const [{ itens }, { saude }] = await Promise.all([api(`${p}/itens`), api(`${p}/saude`)]);
  const parados = new Set(saude.parados.map((x) => x.item));
  const por = Object.fromEntries(COLUNAS.map(([c]) => [c, []]));
  for (const it of itens) {
    if (it.tipo === 'decisao') continue;
    const c = COLUNA_DE[it.estado] || it.estado;
    if (por[c]) por[c].push(it);
  }
  for (const c in por) por[c].sort((a, b) => (data(b.estado_desde) || 0) - (data(a.estado_desde) || 0));
  return `<div class="quadro">${COLUNAS.map(([c, nome]) => {
    const lista = por[c];
    const todas = estado.colunaAberta[c];
    const mostradas = todas ? lista : lista.slice(0, POR_COLUNA);
    const resto = lista.length - mostradas.length;
    return `<section class="coluna"><h3><span>${esc(nome)}</span><span>${lista.length}</span></h3>
      ${mostradas.map((it) => cartaoTarefa(it, parados.has(it.id))).join('') || '<div class="fraco">—</div>'}
      ${resto > 0 ? `<button class="botao" data-coluna="${c}">+ ${resto} mais</button>` : ''}
    </section>`;
  }).join('')}</div>`;
}

function cartaoTarefa(it, parada) {
  const extras = [];
  if (it.estado === 'bloqueada') extras.push(selo('bloqueada', 's-perigo'));
  if (it.estado === 'rejeitada') extras.push(selo('rejeitada', 's-alerta'));
  if (it.prioridade) extras.push(selo(it.prioridade, 'sem-ponto'));
  if (it.categoria === 'bug') extras.push(selo('bug', 's-perigo sem-ponto'));
  return `<div class="tarefa ${parada ? 'parada' : ''}" title="${parada ? 'Parada acima do limite' : ''}">
    <div class="linha entre"><code>${esc(it.id)}</code>${it.tipo === 'missao' ? selo('missão', 's-roxo sem-ponto') : ''}</div>
    <div class="titulo">${esc(it.titulo)}</div>
    <div class="meta">${it.dono ? `<b>${esc(it.dono)}</b>` : '<span>sem dono</span>'}<span>${ha(it.estado_desde)}</span>${extras.join('')}</div>
  </div>`;
}

// ---------- conversa ----------

function montarConversa(id) {
  const f = estado.filtro;
  $('#fixo').innerHTML = `<div class="filtros">
    <input type="search" id="f-quem" placeholder="Vaga ou papel" value="${esc(f.quem)}">
    <input type="search" id="f-item" placeholder="Item (ref)" value="${esc(f.item)}">
    <label class="linha fraco"><input type="checkbox" id="f-sistema" ${f.semSistema ? 'checked' : ''}> esconder CENTRAL</label>
  </div>`;
  const ligar = (sel, campo, prop = 'value') => $(sel).addEventListener('input', (e) => { f[campo] = e.target[prop]; refrescar(true); });
  ligar('#f-quem', 'quem');
  ligar('#f-item', 'item');
  ligar('#f-sistema', 'semSistema', 'checked');
}

async function vistaConversa(id) {
  const p = `/projetos/${enc(id)}`;
  const [hist, { agentes }] = await Promise.all([api(`${p}/mensagens?limite=300`), api(`${p}/agentes`)]);
  const pend = new Map(hist.pendencias.map((m) => [m.seq, m]));
  const f = estado.filtro;
  const quem = f.quem.trim().toUpperCase();
  const item = f.item.trim().toUpperCase();
  const papelDe = Object.fromEntries(agentes.map((a) => [a.vaga, a.papel]));
  const casa = (m) => {
    if (f.semSistema && m.de === 'CENTRAL') return false;
    if (quem && ![m.de, m.para, papelDe[m.de], papelDe[m.para]].some((x) => (x || '').toUpperCase().includes(quem))) return false;
    if (item && !(m.ref || '').toUpperCase().includes(item)) return false;
    return true;
  };
  const ms = hist.mensagens.filter(casa);
  const lista = $('#lista-msg');
  const noFim = !lista || lista.scrollHeight - lista.scrollTop - lista.clientHeight < 60;
  const agora = Date.now();
  const html = ms.map((m) => {
    const p = pend.get(m.seq);
    const vencida = p && data(p.responder_ate) < agora;
    const classes = ['msg', m.de === 'CENTRAL' ? 'sistema' : '', m.de === 'HUMANO' ? 'humano' : '', p ? (vencida ? 'vencida' : 'pendente') : ''].join(' ');
    return `<div class="${classes}">
      <div class="cab"><b>${esc(m.de)}</b><span>→ ${esc(m.para)}</span><span>${esc(m.tipo)}</span>${m.ref ? `<code>${esc(m.ref)}</code>` : ''}
        ${p ? selo(vencida ? 'resposta vencida' : `responder até ${hora(p.responder_ate)}`, vencida ? 's-perigo' : 's-alerta') : ''}
        ${m.responde_a ? `<span>responde #${m.responde_a}</span>` : ''}<span style="margin-left:auto" title="${esc(m.criada_em)}">#${m.seq} · ${hora(m.criada_em)}</span></div>
      <div class="corpo">${esc(m.corpo)}</div></div>`;
  }).join('') || '<p class="vazio">Nenhuma mensagem com esse filtro.</p>';
  if (!$('#form-humano')) {
    // A primeira vez monta a conversa e o formulário; depois só a lista muda.
    $('#dados').innerHTML = `<div class="conversa">
      <div class="mensagens" id="lista-msg"></div>
      <form class="cartao formulario" id="form-humano" data-livre>
        <h2>Mandar como HUMANO</h2>
        <label>Para<select id="h-para" required></select></label>
        <label>Tipo<input id="h-tipo" value="nota" required></label>
        <label>Item (ref, opcional)<input id="h-ref"></label>
        <label>Mensagem<textarea id="h-corpo" required></textarea></label>
        <button class="botao primario" type="submit">Enviar</button>
        <div id="h-status" class="fraco"></div>
      </form></div>`;
    $('#form-humano').addEventListener('submit', (e) => enviarHumano(e, id));
  }
  const sel = $('#h-para');
  const atual = sel.value;
  const destinos = ['TODOS', ...new Set(agentes.filter((a) => a.estado !== 'encerrado').map((a) => a.papel)), ...agentes.filter((a) => a.estado !== 'encerrado').map((a) => a.vaga)];
  sel.innerHTML = [...new Set(destinos)].map((d) => `<option ${d === atual ? 'selected' : ''}>${esc(d)}</option>`).join('');
  const el = $('#lista-msg');
  el.innerHTML = html;
  if (noFim) el.scrollTop = el.scrollHeight;
  return undefined;
}

async function enviarHumano(e, id) {
  e.preventDefault();
  const botao = e.target.querySelector('button');
  const status = $('#h-status');
  botao.disabled = true;
  try {
    await api(`/projetos/${enc(id)}/humano/mensagens`, { method: 'POST', body: {
      para: $('#h-para').value, tipo: $('#h-tipo').value.trim(), ref: $('#h-ref').value.trim(), corpo: $('#h-corpo').value,
    } });
    $('#h-corpo').value = '';
    status.textContent = 'Enviada.';
    refrescar(true);
  } catch (err) {
    status.textContent = err.message;
  } finally {
    botao.disabled = false;
  }
}

// ---------- linha do tempo ----------

function montarLinha() {
  $('#fixo').innerHTML = `<div class="filtros">
    <select id="janela"><option value="1h">Última hora</option><option value="6h">Últimas 6 h</option><option value="24h">Últimas 24 h</option><option value="sprint">Sprint aberta</option></select>
  </div>`;
  const sel = $('#janela');
  sel.value = estado.janela;
  sel.addEventListener('change', () => { estado.janela = sel.value; refrescar(true); });
}

async function vistaLinha(id) {
  const p = `/projetos/${enc(id)}`;
  const ate = new Date();
  let de = new Date(ate - ({ '1h': 1, '24h': 24 }[estado.janela] || 6) * 3600e3);
  if (estado.janela === 'sprint') {
    const { sprint } = await api(`${p}/sprint`);
    if (!sprint) return '<p class="vazio">Nenhuma sprint aberta.</p>';
    de = data(sprint.abertura) || de;
  }
  // O dia anterior à janela dá o estado de cada vaga no começo dela.
  const antes = new Date(de - 24 * 3600e3);
  const [{ eventos }, { agentes }, { recursos }] = await Promise.all([
    api(`${p}/eventos?prefixos=agente.&de=${enc(antes.toISOString())}`), api(`${p}/agentes`), api(`${p}/recursos`),
  ]);
  const series = await Promise.all(Object.keys(recursos).sort().map(async (n) =>
    [n, recursos[n], await api(`${p}/recursos/${enc(n)}/amostras?de=${enc(de.toISOString())}&ate=${enc(ate.toISOString())}`)]));
  return `<div class="cartao secao tempo"><h2>Vagas</h2>${svgVagas(eventos, agentes, de, ate)}
      <div class="legenda">${Object.entries(CORES_ESTADO).map(([e, c]) => `<span><i style="background:${c}"></i>${e}</span>`).join('')}</div></div>
    ${series.map(([n, v, s]) => `<div class="cartao secao tempo"><h2>${esc(n)}</h2>${svgRecurso(v, s, de, ate)}
      <div class="legenda"><span><i style="background:var(--acento)"></i>medido</span><span><i style="background:var(--roxo);opacity:.45"></i>declarado (concessões)</span><span><i style="background:var(--texto-3)"></i>capacidade − reservado</span></div></div>`).join('')}`;
}

function estadoDoEvento(ev, anterior) {
  switch (ev.tipo) {
    case 'agente.entrou': return 'ativo';
    case 'agente.status': return ev.dados?.estado || anterior || 'ativo';
    case 'agente.sumiu': return 'sumido';
    case 'agente.saiu': return 'encerrado';
    default: return anterior;
  }
}

function eixoTempo(de, ate, x0, x1, y) {
  const span = ate - de;
  const passo = span > 12 * 3600e3 ? 3 * 3600e3 : span > 4 * 3600e3 ? 3600e3 : 30 * 60e3;
  let t = Math.ceil(de / passo) * passo;
  let out = '';
  for (; t <= ate; t += passo) {
    const x = x0 + ((t - de) / span) * (x1 - x0);
    out += `<line class="eixo" x1="${x}" x2="${x}" y1="0" y2="${y}"/><text x="${x}" y="${y + 14}" text-anchor="middle">${hora(new Date(t))}</text>`;
  }
  return out;
}

function svgVagas(eventos, agentes, de, ate) {
  const L = 150, W = 1000, H = 22;
  const vagas = agentes.map((a) => a.vaga).sort();
  if (!vagas.length) return '<p class="vazio">Nenhuma vaga.</p>';
  const span = ate - de;
  const x = (t) => L + Math.max(0, Math.min(1, (t - de) / span)) * (W - L);
  let corpo = '';
  vagas.forEach((v, i) => {
    const evs = eventos.filter((e) => e.quem === v && e.tipo.startsWith('agente.'));
    let atual = null;
    let desde = de;
    const faixas = [];
    for (const ev of evs) {
      const t = data(ev.quando);
      const novo = estadoDoEvento(ev, atual);
      if (t <= de) { atual = novo; continue; }
      if (novo !== atual) {
        if (atual) faixas.push([desde, t, atual]);
        atual = novo;
        desde = t;
      }
    }
    if (!atual) atual = agentes.find((a) => a.vaga === v)?.estado;
    if (atual) faixas.push([desde, ate, atual]);
    const y = i * H;
    corpo += `<text x="0" y="${y + 15}">${esc(v)}</text>`;
    corpo += faixas.map(([a, b, e]) => `<rect x="${x(a)}" y="${y + 3}" width="${Math.max(1, x(b) - x(a))}" height="${H - 6}" rx="3" style="fill:${CORES_ESTADO[e] || 'var(--neutro)'}"><title>${esc(v)}: ${esc(e)} ${hora(a)}–${hora(b)}</title></rect>`).join('');
  });
  const alto = vagas.length * H;
  return `<svg viewBox="0 0 ${W} ${alto + 20}" role="img" aria-label="Estados das vagas no tempo">${eixoTempo(de, ate, L, W, alto)}${corpo}</svg>`;
}

function svgRecurso(v, serie, de, ate) {
  const dims = Object.keys(v.spec.dimensoes || {}).sort();
  const W = 1000, H = 120, L = 150;
  const span = ate - de;
  const x = (t) => L + Math.max(0, Math.min(1, (t - de) / span)) * (W - L);
  return dims.map((d) => {
    const cap = v.spec.dimensoes[d] || 1;
    const util = cap - (v.spec.reservado?.[d] || 0);
    const y = (val) => H - Math.max(0, Math.min(1, val / cap)) * H;
    // Declarado: soma das concessões ativas em cada instante (degraus).
    const marcos = new Set([+de, +ate]);
    for (const g of serie.concessoes) {
      if (data(g.inicio)) marcos.add(Math.max(+de, +data(g.inicio)));
      if (data(g.fim)) marcos.add(Math.min(+ate, +data(g.fim)));
    }
    const ts = [...marcos].sort((a, b) => a - b);
    const declarado = (t) => serie.concessoes.reduce((s, g) => {
      const i = data(g.inicio), f = data(g.fim);
      return i && i <= t && (!f || f > t) ? s + (g.declarado?.[d] || 0) : s;
    }, 0);
    let area = `M${x(ts[0])},${H}`;
    ts.forEach((t, k) => {
      const val = declarado(t);
      area += ` L${x(t)},${y(val)}`;
      if (k + 1 < ts.length) area += ` L${x(ts[k + 1])},${y(val)}`;
    });
    area += ` L${x(ts[ts.length - 1])},${H} Z`;
    const pontos = serie.amostras.filter((s) => s.valores?.[d] !== undefined).map((s) => `${x(data(s.quando))},${y(s.valores[d])}`).join(' ');
    return `<div class="fraco mono" style="margin-top:6px">${esc(d)} · capacidade ${num(cap, 0)}</div>
      <svg viewBox="0 0 ${W} ${H + 20}" role="img" aria-label="${esc(d)} medido e declarado">
        ${eixoTempo(de, ate, L, W, H)}
        <path d="${area}" style="fill:var(--roxo);opacity:.3"/>
        <line x1="${L}" x2="${W}" y1="${y(util)}" y2="${y(util)}" style="stroke:var(--texto-3)" stroke-dasharray="4 4"/>
        ${pontos ? `<polyline points="${pontos}" fill="none" style="stroke:var(--acento)" stroke-width="2"/>` : `<text x="${L + 8}" y="${H / 2}">sem leituras no período</text>`}
        <text x="0" y="12">${num(cap, 0)}</text><text x="0" y="${H}">0</text>
      </svg>`;
  }).join('') || '<p class="fraco">Recurso sem dimensões.</p>';
}

// ---------- sprint ----------

async function vistaSprint(id) {
  const p = `/projetos/${enc(id)}`;
  const desde = new Date(Date.now() - 24 * 3600e3).toISOString();
  const [{ sprint }, metricas, { eventos: briefs }] = await Promise.all([
    api(`${p}/sprint`), api(`${p}/metricas`), api(`${p}/eventos?tipos=brief.gerado&de=${enc(desde)}`),
  ]);
  let cer = '<p class="vazio">Nenhuma sprint aberta.</p>';
  if (sprint) {
    const { eventos } = await api(`${p}/eventos?prefixos=cerimonia.&ref=${enc(sprint.id)}`);
    const feitas = new Set(eventos.map((e) => e.tipo.slice('cerimonia.'.length)));
    const prox = proximaCerimonia(sprint);
    cer = `<div class="linha entre"><h2>${esc(sprint.id)}</h2>${sprint.antecipada_em ? selo('antecipada', 's-alerta') : ''}</div>
      <ul class="cerimonias">${['abertura', 'checkin', 'escopo', 'congelamento', 'corte', 'retro', 'fim'].map((n) => {
        const cls = feitas.has(n) ? 'feita' : prox && prox[0] === n ? 'proxima' : '';
        return `<li><span class="hora">${hora(sprint[n])}</span><span class="${cls}">${esc(n)}</span>${feitas.has(n) ? selo('feita', 's-ok') : prox && prox[0] === n ? selo('próxima', 's-ativo') : ''}</li>`;
      }).join('')}</ul>`;
  }
  const m = metricas.metricas;
  const lat = Object.entries(m.latencias || {}).map(([k, l]) =>
    `<tr><td>${esc(k.replaceAll('_', ' → ').replace(' → em → ', ' → em '))}</td><td>${l.n}</td><td>${l.n ? duracao(l.mediana_s * 1000) : '—'}</td><td>${l.n ? duracao(l.p75_s * 1000) : '—'}</td></tr>`).join('');
  const uso = Object.entries(m.uso_recursos || {}).map(([k, v]) => `<div class="linha entre"><span>${esc(k)}</span><b>${num(v, 1)}%</b></div>`).join('');
  const ocio = Object.entries(m.ociosidade_min || {}).sort((a, b) => b[1] - a[1]).map(([k, v]) => `<div class="linha entre"><span>${esc(k)}</span><span>${v} min</span></div>`).join('');
  const porPapel = {};
  for (const e of briefs) porPapel[e.dados?.papel || e.quem] = e;
  const brief = Object.entries(porPapel).sort().map(([papel, e]) =>
    `<tr><td>${esc(papel)}</td><td>${num(e.dados?.tokens, 0)}</td><td>${num(e.dados?.bytes, 0)}</td><td>${e.dados?.curto ? 'curto' : 'completo'}</td><td>${ha(e.quando)}</td></tr>`).join('');
  return `<div class="duas">
    <div class="cartao">${cer}</div>
    <div class="cartao">
      <h2>Métricas ${m.sprint ? `de ${esc(m.sprint)}` : 'das últimas 24 h'}</h2>
      <div class="numeros">
        <div class="numero"><b>${m.integradas}</b><span>integradas</span></div>
        <div class="numero"><b>${m.rejeicoes}</b><span>rejeições</span></div>
        <div class="numero"><b>${m.bloqueios}</b><span>bloqueios</span></div>
        <div class="numero"><b>${num(m.tarefas_por_executor_hora, 2)}</b><span>tarefas por executor·h</span></div>
      </div>
      ${lat ? `<div class="tabela"><table><thead><tr><th>Passagem</th><th>n</th><th>Mediana</th><th>p75</th></tr></thead><tbody>${lat}</tbody></table></div>` : ''}
      ${uso ? `<h3 style="margin-top:12px">Uso de recursos</h3>${uso}` : ''}
      ${ocio ? `<h3 style="margin-top:12px">Ociosidade</h3>${ocio}` : ''}
    </div>
  </div>
  <div class="cartao secao" style="margin-top:14px"><h2>Tamanho do brief por papel (24 h)</h2>
    ${brief ? `<div class="tabela"><table><thead><tr><th>Papel</th><th>Tokens</th><th>Bytes</th><th>Tipo</th><th>Último</th></tr></thead><tbody>${brief}</tbody></table></div>` : '<p class="vazio">Nenhum brief gerado nas últimas 24 h.</p>'}
  </div>`;
}

// ---------- decisões ----------

async function vistaDecisoes(id) {
  const p = `/projetos/${enc(id)}`;
  const { itens } = await api(`${p}/itens?tipo=decisao`);
  const abertas = itens.filter((d) => !FINAIS_DECISAO.includes(d.estado));
  if (!abertas.length) return '<p class="vazio">Nenhuma decisão em aberto.</p>';
  abertas.sort((a, b) => (a.estado === 'humano' ? 0 : 1) - (b.estado === 'humano' ? 0 : 1) || a.id.localeCompare(b.id));
  const detalhes = await Promise.all(abertas.map((d) => api(`${p}/itens/${enc(d.id)}`)));
  return detalhes.map(({ item: d, notas }) => {
    const humano = d.estado === 'humano';
    return `<div class="cartao secao decisao" data-decisao="${esc(d.id)}">
      <div class="linha entre"><div class="linha"><code>${esc(d.id)}</code><h2 style="margin:0">${esc(d.titulo)}</h2></div>
        ${humano ? selo('aguarda você', 's-alerta') : seloEstado(d.estado)}</div>
      <div class="fraco">aberta por ${esc(d.campos?.chave1 || d.dono || '—')} · rodada ${esc(d.campos?.rodada ?? 1)} · ${ha(d.estado_desde)} neste estado</div>
      <div class="corpo">${esc(d.campos?.corpo || '')}</div>
      ${notas.length ? `<div class="notas">${notas.map((n) => `<div><b>${esc(n.vaga)}</b> <span class="fraco">${esc(n.tipo)} · ${hora(n.quando)}</span><div>${esc(n.texto)}</div></div>`).join('')}</div>` : ''}
      <textarea placeholder="${humano ? 'Resposta (opcional para aprovar; obrigatória para vetar)' : 'Motivo do veto'}"></textarea>
      <div class="acoes">
        ${humano ? '<button class="botao ok" data-op="aprovar">Aprovar</button><button class="botao perigo" data-op="recusar">Recusar</button>' : ''}
        <button class="botao perigo" data-op="veto">Vetar</button>
        <span class="fraco" data-status></span>
      </div></div>`;
  }).join('');
}

async function agirDecisao(botao) {
  const cartao = botao.closest('[data-decisao]');
  const id = cartao.dataset.decisao;
  const texto = cartao.querySelector('textarea').value.trim();
  const status = cartao.querySelector('[data-status]');
  const op = botao.dataset.op;
  if (op === 'veto' && !texto) { status.textContent = 'Escreva o motivo do veto.'; return; }
  if (op === 'veto' && !confirm(`Vetar ${id}?`)) return;
  const caminho = `/projetos/${enc(estado.rota.id)}/decisoes/${enc(id)}/${op === 'veto' ? 'veto' : 'responder'}`;
  const body = op === 'veto' ? { motivo: texto } : { aprovada: op === 'aprovar', texto };
  cartao.querySelectorAll('button').forEach((b) => { b.disabled = true; });
  try {
    await api(caminho, { method: 'POST', body });
    cartao.querySelector('textarea').value = '';
    refrescar(true);
  } catch (e) {
    status.textContent = e.message;
    cartao.querySelectorAll('button').forEach((b) => { b.disabled = false; });
  }
}

// ---------- extensões ----------

function extDe(id, nome) {
  return (estado.exts[id] || []).find((x) => x.nome === nome);
}

function montarExt(id, nome) {
  $('#fixo').innerHTML = `<div class="linha entre secao">
    <div class="linha"><h2 style="margin:0" id="ext-titulo">${esc(nome)}</h2><span class="fraco" id="ext-ms"></span></div>
    <div class="linha"><button class="botao" data-ext-atualizar>Atualizar</button></div>
  </div><div id="ext-entrada"></div>`;
}

// vistaExt chama a ação da aba só quando forçado (abrir, Atualizar, depois de
// um formulário): cada chamada roda um processo. Os eventos ao vivo só
// atualizam as pendências da pasta de entrada.
async function vistaExt(id, forcar) {
  const nome = estado.rota.ext;
  if (!estado.exts[id]) estado.exts[id] = (await api(`/projetos/${enc(id)}/extensoes`)).extensoes;
  const x = extDe(id, nome);
  if (!x || !x.aba) return `<p class="vazio">A extensão ${esc(nome)} não tem aba no central.toml.</p>`;
  $('#ext-titulo').textContent = x.aba.titulo;
  if (x.entrada && !$('#ext-soltar')) $('#ext-entrada').innerHTML = zonaEntrada(x);
  const chave = `${id}/${nome}`;
  let erroAba = '';
  if (forcar || !(chave in estado.extDados)) {
    try {
      estado.extDados[chave] = await api(`/projetos/${enc(id)}/extensoes/${enc(nome)}/${enc(x.aba.acao)}`, { method: 'POST', body: { args: {} } });
    } catch (e) {
      erroAba = `<div class="erro">${esc(e.message)}</div>`;
      estado.extDados[chave] = null;
    }
  }
  const r = estado.extDados[chave];
  $('#ext-ms').textContent = r ? `${x.aba.acao} · ${r.ms} ms` : '';
  let pend = '';
  if (x.entrada) {
    const { entrada } = await api(`/projetos/${enc(id)}/extensoes/${enc(nome)}/pendentes`);
    const alvo = $(`#abas-ext [data-ext="${CSS.escape(nome)}"]`);
    if (alvo) alvo.innerHTML = entrada.pendentes.length ? selo(entrada.pendentes.length, 's-alerta sem-ponto') : '';
    pend = (entrada.erro ? `<div class="erro">A última entrega da pasta ${esc(entrada.pasta)} falhou: ${esc(entrada.erro)}</div>` : '') +
      entrada.pendentes.map((p) => `<div class="cartao secao">
        <div class="linha entre"><h3 style="margin:0">Chegou na pasta de entrada</h3><span class="fraco">${ha(p.desde)}</span></div>
        <div class="fraco mono" style="margin:4px 0 8px">${esc((p.args.arquivos || []).join(', '))}</div>
        ${formulario(p.formulario, { pendente: p.id })}</div>`).join('');
  }
  let corpo = '';
  if (r) {
    // O formulário pedido pela ação vem acima da visão; os dois aparecem.
    if (r.formulario) corpo = `<div class="cartao secao">${formulario(r.formulario, { acao: x.aba.acao })}</div>`;
    if (x.aba.visao === 'formulario') {
      if (!r.formulario) corpo = `<div class="cartao secao">${formulario(r.dados, { acao: x.aba.acao })}</div>`;
    } else {
      corpo += VISOES_EXT[x.aba.visao](r.dados, id);
    }
  }
  return erroAba + pend + corpo;
}

function zonaEntrada(x) {
  return `<label class="soltar secao" id="ext-soltar">
    <input type="file" multiple hidden data-ext-arquivos>
    <b>Solte arquivos aqui</b> <span class="suave">ou clique para escolher · vão para <code>${esc(x.entrada.pasta)}</code></span>
    <div class="fraco" data-ext-envio></div>
  </label>`;
}

async function enviarArquivos(files) {
  const { id, ext: nome } = estado.rota;
  const status = $('[data-ext-envio]');
  for (const f of files) {
    status.textContent = `Enviando ${f.name}…`;
    try {
      const r = await fetch(`/v1/projetos/${enc(id)}/extensoes/${enc(nome)}/entrada/${enc(f.name)}`, { method: 'PUT', body: f });
      const j = await r.json().catch(() => ({}));
      if (!j.ok) throw new Error(j.erro?.mensagem || `HTTP ${r.status}`);
      status.textContent = `${f.name} enviado; a extensão recebe em instantes.`;
    } catch (e) {
      status.textContent = `${f.name}: ${e.message}`;
      return;
    }
  }
  setTimeout(() => refrescar(false), 2500);
}

// formulario desenha os campos pedidos por uma extensão. destino diz o que o
// envio chama: a pendência da entrada ou a ação da aba.
function formulario(f, destino) {
  if (!f) return '';
  const campos = (f.campos || []).map((c) => {
    const req = c.obrigatorio ? 'required' : '';
    const valor = esc(c.valor ?? '');
    let input;
    switch (c.tipo) {
      case 'longo': input = `<textarea name="${esc(c.nome)}" ${req}>${valor}</textarea>`; break;
      case 'numero': input = `<input type="number" step="any" name="${esc(c.nome)}" value="${valor}" ${req}>`; break;
      case 'escolha': input = `<select name="${esc(c.nome)}" ${req}>${(c.opcoes || []).map((o) => {
        const [v, r] = typeof o === 'object' ? [o.valor, o.rotulo ?? o.valor] : [o, o];
        return `<option value="${esc(v)}" ${String(v) === String(c.valor ?? '') ? 'selected' : ''}>${esc(r)}</option>`;
      }).join('')}</select>`; break;
      default: input = `<input type="text" name="${esc(c.nome)}" value="${valor}" ${req}>`;
    }
    return `<label>${esc(c.rotulo || c.nome)}${c.obrigatorio ? ' *' : ''}${input}${c.ajuda ? `<span class="fraco">${esc(c.ajuda)}</span>` : ''}</label>`;
  }).join('');
  const alvo = destino.pendente ? `data-pendente="${destino.pendente}"` : `data-acao="${esc(destino.acao)}"`;
  return `<form class="formulario-ext" ${alvo}>
    ${f.titulo ? `<h2>${esc(f.titulo)}</h2>` : ''}${f.descricao ? `<p class="suave">${esc(f.descricao)}</p>` : ''}
    ${campos}<div class="acoes"><button class="botao primario" type="submit">${esc(f.enviar || 'Enviar')}</button><span class="fraco" data-status></span></div>
  </form>`;
}

async function enviarFormulario(form) {
  const { id, ext: nome } = estado.rota;
  const campos = {};
  for (const el of form.elements) {
    if (!el.name) continue;
    campos[el.name] = el.type === 'number' && el.value !== '' ? Number(el.value) : el.value;
  }
  const status = form.querySelector('[data-status]');
  const botao = form.querySelector('button');
  botao.disabled = true;
  status.textContent = 'Enviando…';
  try {
    if (form.dataset.pendente) {
      await api(`/projetos/${enc(id)}/extensoes/${enc(nome)}/pendentes/${form.dataset.pendente}`, { method: 'POST', body: { campos } });
    } else {
      estado.extDados[`${id}/${nome}`] = await api(`/projetos/${enc(id)}/extensoes/${enc(nome)}/${enc(form.dataset.acao)}`, { method: 'POST', body: { args: campos } });
    }
    // Esvazia antes de redesenhar: o refresco não apaga campo preenchido.
    for (const el of form.elements) if (el.name) el.value = '';
    refrescar(true);
  } catch (e) {
    status.textContent = e.message;
    botao.disabled = false;
  }
}

function srcArquivo(id, caminho) {
  if (/^data:image\//.test(caminho)) return caminho;
  return `/v1/projetos/${enc(id)}/arquivo?caminho=${enc(caminho)}`;
}

function celula(v) {
  if (v === null || v === undefined) return '<span class="fraco">—</span>';
  if (typeof v === 'object') return `<code>${esc(JSON.stringify(v))}</code>`;
  return esc(v);
}

const VISOES_EXT = {
  tabela(d) {
    let colunas, linhas;
    if (d && Array.isArray(d.colunas)) {
      colunas = d.colunas;
      linhas = d.linhas || [];
    } else {
      const lista = Array.isArray(d) ? d : [];
      colunas = [...new Set(lista.flatMap((o) => Object.keys(o || {})))];
      linhas = lista.map((o) => colunas.map((c) => o?.[c]));
    }
    if (!linhas.length) return '<p class="vazio">Nada para mostrar.</p>';
    return `<div class="cartao tabela"><table><thead><tr>${colunas.map((c) => `<th>${esc(c)}</th>`).join('')}</tr></thead>
      <tbody>${linhas.map((l) => `<tr>${l.map((v) => `<td>${celula(v)}</td>`).join('')}</tr>`).join('')}</tbody></table></div>`;
  },
  galeria(d, id) {
    const itens = Array.isArray(d) ? d : d?.itens || [];
    if (!itens.length) return '<p class="vazio">Nada para mostrar.</p>';
    return `<div class="galeria">${itens.map((it) => {
      const midia = !it.imagem ? '<div class="sem-midia">sem imagem</div>'
        : /\.(mp4|webm)$/i.test(it.imagem) ? `<video src="${esc(srcArquivo(id, it.imagem))}" controls preload="metadata"></video>`
          : `<img src="${esc(srcArquivo(id, it.imagem))}" alt="${esc(it.legenda || '')}" loading="lazy">`;
      return `<figure class="cartao">${midia}<figcaption><b>${esc(it.legenda || '')}</b>${it.detalhe ? `<div class="fraco">${esc(it.detalhe)}</div>` : ''}
        <div class="linha">${(it.selos || []).map((s) => selo(s, 'sem-ponto')).join('')}</div></figcaption></figure>`;
    }).join('')}</div>`;
  },
  serie(d) {
    const series = (d?.series || []).filter((s) => (s.pontos || []).length);
    if (!series.length) return '<p class="vazio">Nenhum ponto.</p>';
    const tempo = series.some((s) => s.pontos.some((p) => typeof p.x === 'string'));
    const xv = (p) => (tempo ? +new Date(p.x) : Number(p.x));
    const todos = series.flatMap((s) => s.pontos);
    const [x0, x1] = [Math.min(...todos.map(xv)), Math.max(...todos.map(xv))];
    const [y0, y1] = [Math.min(0, ...todos.map((p) => p.y)), Math.max(...todos.map((p) => p.y))];
    const W = 1000, H = 220, L = 60;
    const x = (v) => L + (x1 === x0 ? 0.5 : (v - x0) / (x1 - x0)) * (W - L - 10);
    const y = (v) => 10 + (1 - (y1 === y0 ? 0.5 : (v - y0) / (y1 - y0))) * (H - 30);
    const cores = ['var(--acento)', 'var(--ok)', 'var(--alerta)', 'var(--roxo)', 'var(--perigo)', 'var(--neutro)'];
    const rotX = (v) => (tempo ? `${new Date(v).toLocaleDateString('pt-BR', { day: '2-digit', month: '2-digit' })} ${hora(new Date(v))}` : num(v, 2));
    const linhas = series.map((s, i) => {
      const pts = [...s.pontos].sort((a, b) => xv(a) - xv(b));
      const cor = cores[i % cores.length];
      return `<polyline fill="none" stroke-width="2" style="stroke:${cor}" points="${pts.map((p) => `${x(xv(p))},${y(p.y)}`).join(' ')}"/>` +
        pts.map((p) => `<circle cx="${x(xv(p))}" cy="${y(p.y)}" r="3.5" style="fill:${cor}"><title>${esc(s.nome)}: ${esc(p.rotulo || num(p.y, 3))} · ${esc(rotX(xv(p)))}</title></circle>`).join('');
    }).join('');
    return `<div class="cartao tempo"><svg viewBox="0 0 ${W} ${H}" role="img" aria-label="série">
      <line class="eixo" x1="${L}" x2="${W - 10}" y1="${H - 20}" y2="${H - 20}"/>
      <text x="${L - 6}" y="${y(y1) + 4}" text-anchor="end">${num(y1, 2)}</text><text x="${L - 6}" y="${y(y0) + 4}" text-anchor="end">${num(y0, 2)}</text>
      <text x="${L}" y="${H - 4}">${esc(rotX(x0))}</text><text x="${W - 10}" y="${H - 4}" text-anchor="end">${esc(rotX(x1))}</text>
      ${linhas}</svg>
      <div class="legenda">${series.map((s, i) => `<span><i style="background:${cores[i % cores.length]}"></i>${esc(s.nome)}</span>`).join('')}</div></div>`;
  },
};

// ---------- ligação ----------

const VISTAS = { agora: vistaAgora, quadro: vistaQuadro, conversa: vistaConversa, linha: vistaLinha, sprint: vistaSprint, decisoes: vistaDecisoes, ext: vistaExt };

document.addEventListener('submit', (e) => {
  const form = e.target.closest('.formulario-ext');
  if (form) { e.preventDefault(); enviarFormulario(form); }
});
document.addEventListener('change', (e) => {
  if (e.target.matches('[data-ext-arquivos]')) enviarArquivos([...e.target.files]);
});
document.addEventListener('dragover', (e) => {
  const z = e.target.closest('#ext-soltar');
  if (z) { e.preventDefault(); z.classList.add('sobre'); }
});
document.addEventListener('dragleave', (e) => { e.target.closest('#ext-soltar')?.classList.remove('sobre'); });
document.addEventListener('drop', (e) => {
  const z = e.target.closest('#ext-soltar');
  if (!z) return;
  e.preventDefault();
  z.classList.remove('sobre');
  enviarArquivos([...e.dataTransfer.files]);
});
document.addEventListener('click', (e) => {
  if (e.target.closest('[data-ext-atualizar]')) { refrescar(true); return; }
  const col = e.target.closest('[data-coluna]');
  if (col) { estado.colunaAberta[col.dataset.coluna] = true; refrescar(true); return; }
  const vagaOp = e.target.closest('[data-vaga-op]');
  if (vagaOp) { agirVaga(vagaOp); return; }
  const op = e.target.closest('[data-op]');
  if (op) agirDecisao(op);
});
window.addEventListener('hashchange', aoMudarRota);
setInterval(() => { if (!estado.rota.id) refrescar(false); }, REFRESCO_MAQUINA_MS);
setInterval(() => { if (estado.rota.id) agendar(); }, REFRESCO_RELOGIO_MS);
aoMudarRota();
