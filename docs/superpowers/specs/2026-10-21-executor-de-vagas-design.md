# Executor de vagas: a Central abre e supervisiona os agentes

Data: 2026-10-21 · Versão alvo: Central 1.3.0 · Estado: proposta para revisão

## 1. Por que existe
Hoje uma pessoa abre a sessão de cada agente. Num time, o Gerente-agente abre as demais pela ferramenta do app, e o app recusa abrir sessões em turnos automáticos. Uma vaga que falta ou que some depende de um clique humano. O objetivo é **tirar o humano do trabalho de abrir e reabrir sessões**.

**Critério de sucesso:**
- uma sprint inteira roda sem nenhum clique humano depois da abertura;
- uma vaga derrubada (processo morto, turno travado ou reinício do daemon) volta a trabalhar sozinha em até 15 min.

## 2. Decisões tomadas (com o humano)
| # | Decisão |
|---|---|
| E1 | Os agentes rodam como **Claude Code CLI em segundo plano**, não como sessões do app. O humano acompanha pelo painel e abre uma sessão no app quando quiser (`claude --desktop --resume <id>`). |
| E2 | **A Central conduz os turnos.** Cada vaga é uma sessão do Claude Code, e a Central roda um turno (`claude -p --resume`) sempre que a vaga acordaria. Não há `/loop` nem `esperar` em segundo plano para essas vagas. |
| E3 | **O Gerente-agente decide a equipe** (`central vaga abrir/fechar`), sempre dentro do teto por cargo do `central.toml`. A Central reabre sozinha uma vaga que cai. |
| E4 | **Permissão no modo automático:** `--permission-mode auto` e `--permission-prompts none`. Um pedido que ninguém pode responder é negado e registrado, nunca trava a vaga. |
| E5 | **Falhas:** um turno pode falhar até 3 vezes seguidas, com nova tentativa depois de 1, 5 e 15 min. Na terceira falha a vaga é suspensa. Quando o limite de uso do plano é atingido, todas as vagas pausam até o reset. |

**Fora de escopo:**
- outras máquinas;
- o runtime do app desktop;
- aprovar permissões pelo painel (a `--permission-prompt-tool` fica para depois);
- qualquer projeto específico.

## 3. Arquitetura
Fronteiras seguindo a spec principal (§4): `core` é o único que toca no banco, `service` orquestra e as interfaces não têm lógica.

### 3.1 `internal/executor` (novo, sem banco)
`Rodar(ctx, Turno) (Resultado, error)` executa um turno do Claude Code e devolve o que ele informou.
- **`Turno`:** comando do claude, pasta, id da sessão, se é o primeiro turno, nome do worktree, modelo, esforço, texto, prazo e ambiente extra.
- **Linha do primeiro turno:** `claude -p --session-id <uuid> -w <vaga> --model M --effort E --permission-mode auto --permission-prompts none --output-format stream-json --verbose "<texto>"`, rodada na raiz do projeto.
- **Turnos seguintes:** `claude -p --resume <uuid>` com as mesmas opções, sem `-w`, rodados no worktree da vaga.
- **Execução:** pelo `internal/shell`, para o cancelamento matar a árvore de processos, com prazo. O stdout é lido em fluxo e guardado num log por turno (`<dados>/projetos/<id>/turnos/<vaga>/<n>.jsonl`, até 20 MB, o resto é cortado).
- **`Resultado`**, tirado das mensagens `system` (init) e `result` do stream:
  - id da sessão e pasta (`cwd`);
  - texto final (os últimos 2 KB);
  - custo estimado (`total_cost_usd`), tokens (`usage`) e número de turnos do modelo;
  - permissões negadas (`permission_denials`);
  - código de saída;
  - classificação da falha: `ok`, `prazo`, `limite_plano` (mensagem de limite de uso, com a hora de reset quando houver), `erro` ou `sem_claude`.

### 3.2 `core`
- **Tabela `vagas_exec`:** vaga (chave), papel, área, nota, sessão, pasta, modelo, esforço, estado (`aberta`, `fechada` ou `suspensa`), falhas seguidas, próximo turno permitido em, pid e início do turno em curso (vazios sem turno), aberta em, fechada em.
- **Tabela `despertares`:** vaga, quando, motivo, feito.
- **Eventos:**
  - de vaga: `vaga.aberta`, `vaga.fechada`, `vaga.suspensa`, `vaga.retomada`;
  - de turno: `turno.iniciado` (vaga, n, motivo), `turno.terminou` (duração, custo, tokens, negações, resumo do resultado) e `turno.falhou` (motivo, código, fim do stderr);
  - do plano: `plano.pausado` e `plano.retomado`.
- **Transações:** abrir, fechar, iniciar e terminar um turno são transações curtas com evento, como todo o núcleo (regra 4 da spec principal).
- **Estado derivado de uma vaga executada:** ela não tem batimento entre turnos, então nunca é marcada como `sumido`.
  - com turno em curso: `trabalhando`;
  - `aberta` sem turno: `ocioso`;
  - `suspensa`: o novo estado `suspenso`;
  - `fechada`: `encerrado`.
  
  A saúde e as órfãs usam esse estado. Uma vaga executada só "some" se for suspensa, e aí a saúde mostra o alerta.

### 3.3 `service`: o escalonador
`RunExecutor(ctx, intervalo)` roda no daemon, como o medidor e a pasta de entrada, com tique de 2 s e acordado pelo `Changed()` do núcleo. A cada passo, para cada projeto com `[execucao]`:
1. **Recuperação:** cada vaga com turno em curso cujo pid já não existe ganha `turno.falhou` (`daemon_reiniciou` ou `processo_sumiu`).
2. **Escolha dos turnos:** uma vaga `aberta`, sem turno em curso, com `agora ≥ próximo turno permitido` e sem pausa do plano, recebe um turno se:
   - tem mensagem não lida (cerimônias, concessões de recurso, respostas e avisos de órfãs já chegam como mensagem);
   - tem despertar vencido;
   - ou é o primeiro turno.
3. **Fila global:** no máximo `max_simultaneos` turnos ao mesmo tempo. O GERENTE sai primeiro e depois as demais vagas, pelo tempo de espera.
4. **Texto do turno:** sai só de modelos fixos.
   - Primeiro turno: quem é a vaga, `central entrar …`, `central brief --md`, a nota do Gerente e a regra do fim de turno.
   - Turnos seguintes: `Você acordou: <motivo> (<n> mensagens). Comece por central caixa.`
   - Depois de uma falha: `O turno anterior terminou por <motivo>; confira onde parou.`
5. **Ao terminar:**
   - `ok`: zera as falhas seguidas.
   - `prazo` ou `erro`: soma uma falha e espera 1, 5 ou 15 min. Na terceira falha a vaga fica `suspensa`, e o GERENTE recebe `vaga.suspensa`.
   - `limite_plano`: grava `plano.pausado` até a hora de reset (ou agora + 30 min), sem contar como falha da vaga.
   - Teto diário de custo estimado, se configurado: ao ser passado, pausa todas as vagas até 00:00 local, com aviso.

O turno roda numa goroutine. O pid e o início ficam gravados para a recuperação. O processo do Claude Code não depende do daemon, então um daemon que reinicia não derruba um turno vivo, e a recuperação o reconhece pelo pid.

### 3.4 `central.toml`
```toml
[execucao]
claude          = "claude"          # comando do Claude Code CLI
max_simultaneos = 4                 # turnos ao mesmo tempo, no projeto
prazo_turno     = "2h"              # padrão por cargo
custo_dia_usd   = 0                 # 0 = sem teto
[execucao.cargos]
GERENTE    = { max = 1, modelo = "sonnet", esforco = "medium" }
EXECUTOR   = { max = 2, modelo = "opus",   esforco = "high", prazo_turno = "3h" }
REVISOR    = { max = 2, modelo = "opus",   esforco = "high" }
```
- As chaves de cargo aceitam o nome do projeto ou o canônico, como `[papeis]`.
- Um cargo sem entrada não pode ser aberto pelo executor.
- Sem `[execucao]`, nada muda para o projeto.

### 3.5 Interfaces
- **CLI:**
  - `central vaga abrir <VAGA> --papel P [--area A] [--nota T]` e `central vaga fechar <VAGA>`: só o GERENTE ou o humano. Abrir respeita o teto do cargo; reabrir uma vaga fechada ou suspensa retoma a mesma sessão.
  - `central vagas`: lista as vagas com estado, turno em curso, custo do dia e sessão.
  - `central despertar --em 20m "<motivo>"`: a vaga pede um turno com hora, para quando espera algo que não gera mensagem.
  - `central sprint abrir` também abre a vaga GERENTE1, se o projeto tem `[execucao]` e ela ainda não está aberta. O fim da sprint fecha todas as vagas.
- **API `/v1`, só rotas novas:**
  - `POST /v1/projetos/{id}/vagas`, `POST /v1/projetos/{id}/vagas/{vaga}/fechar` e `GET /v1/projetos/{id}/vagas`;
  - `POST /v1/projetos/{id}/despertar`;
  - `POST /v1/projetos/{id}/vagas/{vaga}/app`, que roda `claude --desktop --resume <sessão>`.
- **MCP:** ferramentas `vaga_abrir`, `vaga_fechar`, `vagas` e `despertar`.
- **Painel, aba Agora:**
  - nas vagas executadas: turno em curso ("turno desde 10:42"), custo do dia, última negação, e os botões Fechar, Reabrir e Abrir no app;
  - faixa de aviso quando o plano está pausado.
- **Brief:** o fim de ciclo das vagas executadas passa a ser *"Ao terminar uma unidade de trabalho, termine o turno: a Central acorda você quando houver algo. Para esperar algo sem mensagem, use `central despertar`."* A skill `central` do plugin ganha a mesma variante.
- **`doctor`:**
  - `executor`: o comando `claude` resolve;
  - `executor:login`: `claude auth status` sai com 0.

## 4. Segurança
- Abrir e fechar vagas é só do GERENTE (e do humano). O teto vem do `central.toml`.
- O texto dos turnos sai só de modelos fixos mais dados da Central: motivo, contagem e a nota do Gerente, que é uma vaga do próprio projeto.
- Permissão sempre `auto` com `--permission-prompts none`. Uma negação é registrada e não trava a vaga.
- O custo estimado é registrado por turno, com teto diário opcional.
- Os logs dos turnos ficam na pasta de dados da Central, que é local.

## 5. Testes
- **`claude` falso** (`examples/claude-falso`, em Go): lê os argumentos, grava o que recebeu para o teste conferir e responde um stream-json gravado. Por variável de ambiente, ele pode demorar, falhar, devolver limite de uso com hora de reset, negar uma permissão ou criar o worktree.
- **Testes de `executor`:** argumentos do primeiro turno e dos seguintes, leitura do stream (id, custo, tokens, negações), classificação de cada falha e prazo que mata a árvore de processos.
- **Testes de `service`:**
  - turno por mensagem, despertar e primeiro turno;
  - nenhum turno sobreposto na mesma vaga e respeito ao `max_simultaneos`, com o GERENTE primeiro;
  - 3 falhas levam à suspensão, e o limite do plano pausa todas;
  - recuperação depois de reiniciar (pid vivo é reconhecido, pid morto vira falha);
  - teto do cargo e só o GERENTE abre;
  - teto de custo.
- **Testes de CLI e e2e:**
  - `vaga abrir/fechar/vagas`;
  - `sprint abrir` abre o GERENTE1 e o fim da sprint fecha tudo;
  - `doctor` do executor.
- **Teste real opcional** (`CENTRAL_TESTE_CLAUDE_REAL=1`): um turno com `--model haiku` e o texto "responda ok", para conferir os argumentos contra o CLI de verdade. Fica desligado no CI.

## 6. Entrega
Central 1.3.0, em três PRs, cada um com TDD, revisão final e fast-forward com o e-mail noreply:
1. `executor` e o escalonador;
2. comandos `vaga` e `despertar`, a abertura e o fim da sprint, MCP e brief;
3. painel, `doctor`, documentação e release.
