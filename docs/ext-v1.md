# Contrato `ext/v1`

Uma extensão é um programa qualquer declarado no `central.toml`. A Central o chama com uma linha JSON no stdin e lê uma linha JSON do stdout. Mudar os campos abaixo exige `ext/v2`, com o `ext/v1` mantido por pelo menos uma versão maior.

## Declaração
```toml
[[extensoes]]
nome    = "exemplos"                                   # [a-z][a-z0-9-]*, não pode ser um comando da central
comando = "resources/runtime/python.exe -X utf8 -I .central/ext/exemplos.py"
timeout = "10m"                                        # de 1s a 60m; padrão 60s
cli     = ["listar", "importar", "status"]             # viram: central exemplos <acao>
aba     = { titulo = "Exemplos", visao = "galeria", acao = "listar" }   # visao: tabela, galeria, serie, formulario
entrada = { pasta = "artifacts/entrada", ao_chegar = "importar" }       # relativa à raiz, sem ..

[acordar]
exemplo-novo = ["PESQUISADOR"]                         # tipo de evento → papéis (ou vagas, ou TODOS)
```
Só as ações em `cli`, `aba.acao` e `entrada.ao_chegar` podem ser chamadas, tanto por agentes quanto pelo humano no painel. Qualquer outra é recusada.

## Chamada
- **Execução:** o comando roda num shell (`cmd /S /C` no Windows, `sh -c` no resto), com a **raiz registrada do projeto** como pasta atual.
- **Ambiente:** além do ambiente do daemon, vão `CENTRAL_EXT=ext/v1`, `CENTRAL_PROJETO=<id>`, `CENTRAL_ADDR=<host:porta>` e `PYTHONUTF8=1`.
- **Codificação:** stdin e stdout são **UTF-8**. No Windows, o Python com `-I` ignora o `PYTHONUTF8`; use `-X utf8` (ou `sys.stdout.reconfigure(encoding="utf-8")` e o mesmo no stdin).
- **Concorrência:** há uma chamada por vez por extensão e projeto.
- **Fim da chamada:** passado o `timeout`, a Central mata a árvore de processos. Mais de 1 MB no stdout é erro. O stderr não conta para a resposta: o fim dele (até 64 KB) entra na mensagem de erro.

### Pedido (stdin, uma linha)
```json
{"versao":"ext/v1","acao":"importar","args":{"arquivos":["artifacts/entrada/a.mp4"]},
 "projeto":{"id":"lyriccut","nome":"LyricCut","raiz":"C:/…/CortesEditor"},
 "agente":{"vaga":"PESQ1","papel":"PESQUISADOR"}}
```
`agente` vem `null` quando quem chama é o humano (painel) ou a própria Central (pasta de entrada). O golden fica em `internal/ext/testdata/pedido.golden.json`.

### Resposta (stdout: a última linha não vazia)
```json
{"ok":true,"dados":…,"eventos":[{"tipo":"exemplo-novo","ref":"digimon","dados":{"resumo":"conjunto digimon importado"}}],"formulario":null}
```
- **Linhas antes da resposta:** o que vier antes da última linha é log e é ignorado.
- **`ok:false`:** responda `ok:false` com `"erro":{"mensagem":"…"}`. A chamada falha com o código `extensao` (HTTP 502, saída 1 da CLI) e o resto da Central segue.
- **`eventos`:**
  - cada evento vira o evento `ext.<tipo>` do projeto, com `dados.extensao`;
  - o `tipo` aceita `[a-z0-9][a-z0-9._-]*`;
  - cada destino em `[acordar]` para o tipo recebe uma mensagem de `CENTRAL` com tipo `ext.<tipo>` e a ref do evento. O corpo é `dados.resumo` (ou um texto padrão), e o `esperar` acorda com motivo `ext`.
- **`fatos`** (opcional): `[{"chave":"e2e.abc1234-1","valor":{…},"motivo":"…"}]`.
  - Cada um vira uma versão do fato do projeto, na mesma transação dos eventos. Quem grava é a vaga que chamou, o `HUMANO` ou a `CENTRAL`.
  - A resposta inteira é inválida se uma chave sair do formato (`[a-z0-9][a-z0-9._-]{0,127}`), um valor não for JSON ou um motivo vier vazio.
  - O campo entrou depois do `ext/v1` original e é aditivo: quem não o usa não muda nada.
- **Registro:** toda chamada grava `ext.chamada` (`extensao`, `acao`, `ok`, `ms` e `erro`).

### Ler a Central de dentro de uma extensão
As rotas de leitura de `/v1` não precisam de pasta. Por exemplo, `GET http://$CENTRAL_ADDR/v1/projetos/$CENTRAL_PROJETO/fatos` lista os fatos vigentes, e `…/fatos/{chave}/historico` traz as versões de um. Para escrever, devolva `eventos` e `fatos` na resposta, em vez de chamar a API.

### `formulario`
```json
{"titulo":"Novo vídeo-meta","descricao":"…","enviar":"Importar","campos":[
  {"nome":"nome","rotulo":"Nome","tipo":"texto","obrigatorio":true,"valor":"digimon"},
  {"nome":"perfil","rotulo":"Perfil","tipo":"escolha","opcoes":["anime","lyric"]},
  {"nome":"ensina","rotulo":"O que ensina","tipo":"longo","ajuda":"uma ou duas frases"}]}
```
Os tipos de campo são `texto`, `numero`, `escolha` (`opcoes` como texto ou `{valor, rotulo}`) e `longo`. O painel pede os campos ao humano e chama **a mesma ação** com os args originais mais os campos.

## Visões da aba
A aba chama `aba.acao` com `args: {}` quando abre e quando o humano clica em Atualizar.

| Visão | `dados` |
|---|---|
| `tabela` | uma lista de objetos (as colunas são as chaves) ou `{"colunas":[…],"linhas":[[…]]}` |
| `galeria` | uma lista de `{"imagem","legenda","detalhe","selos":[…]}`. `imagem` é um caminho relativo à raiz (png, jpg, gif, webp, mp4, webm) ou `data:image/…` |
| `serie` | `{"series":[{"nome","pontos":[{"x","y","rotulo"}]}]}`, com `x` sendo um horário RFC 3339 ou um número |
| `formulario` | o próprio formulário (o envio chama `aba.acao` com os campos) |

## Pasta de entrada
1. O daemon olha a pasta a cada 2 s. Um arquivo é entregue quando o tamanho e o horário dele não mudam entre duas leituras.
2. Os arquivos prontos vão juntos para `ao_chegar` com `{"arquivos":[caminhos relativos à raiz, com /]}`. Quem chama é a `CENTRAL` (`agente: null`).
3. Nomes começando com `.` são ignorados. O upload do painel (`PUT /v1/projetos/{id}/extensoes/{nome}/entrada/{arquivo}`, até 8 GiB) grava `.central-parcial-<nome>` e renomeia no fim.
4. O arquivo entregue não é entregue de novo enquanto o daemon estiver no ar. A extensão deve **tirar o arquivo da pasta** quando terminar.
5. Uma resposta com `formulario` fica pendente na aba (`GET …/pendentes`, `POST …/pendentes/{id}`) até o humano responder.
6. Se o daemon reiniciar antes, o arquivo ainda está na pasta e é entregue de novo.

## Exemplo
`examples/ext/eco` (Go) responde os args e serve de teste do contrato. Para testar uma extensão sua sem a Central:
```powershell
'{"versao":"ext/v1","acao":"listar","args":{},"projeto":{"id":"p","nome":"P","raiz":"."},"agente":null}' | python -X utf8 -I .central/ext/exemplos.py
```
