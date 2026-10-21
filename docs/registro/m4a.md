# Registro do M4a (extensões `ext/v1`)

Execução nativa (sem subagentes), plano em `docs/plans/m4a-extensoes.md`, branch `m4a-extensoes`. O contrato completo está em `docs/ext-v1.md`.

## Decisões tomadas
- **Código `extensao` (HTTP 502, saída 1 da CLI).** Uma extensão que falha (prazo, saída grande, resposta inválida ou `ok:false`) dá esse código. Uma ação fora da lista declarada dá `invalido` (422, saída 4). Assim "a extensão falhou" não se confunde com "o pedido está errado". Custo se errado: um código a mais no contrato `/v1`.
- **`/arquivo` serve mp4 e webm, além de imagens.** A galeria do LyricCut mostra vídeos-meta, e o vídeo não executa script. SVG fica de fora, porque rodaria script na origem do painel.
- **UTF-8.** A Central põe `PYTHONUTF8=1` no ambiente, e o contrato declara stdin e stdout em UTF-8. O `python -I` ignora variáveis `PYTHON*`, então a doc pede `python -X utf8 -I`. Achei isso na verificação visual: os acentos chegavam em cp1252.
- **`shellCommand` foi para `internal/shell`.** O medidor e as extensões rodam comandos pelo mesmo caminho, e o cancelamento mata a árvore.
- **A pasta de entrada vive em memória.** Isso cobre os arquivos já entregues, as pendências e o último erro. Depois de um reinício, os arquivos ainda estão na pasta e a entrega recomeça sozinha.

## Verificação visual
Usei uma extensão de demonstração em Python (galeria com pasta de entrada e formulário, mais uma série) no daemon de demonstração. Fiz o upload de uma imagem pelo mesmo caminho do soltar de arquivos. A pendência apareceu com o formulário. Depois do envio, o item entrou na galeria com a imagem servida por `/arquivo`. A série renderizou com duas linhas.

## Achados da revisão final (corrigidos)
- O vigia recriava a pasta de entrada numa raiz que não existe mais (`TestEntradaNaoCriaRaizQueSumiu`).
- Duas respostas ao mesmo formulário pendente rodavam a importação duas vezes (`TestFormularioRespondidoDuasVezes`).

## Pequenos adiados
- O painel não esconde o botão Enviar de uma pendência que já está sendo respondida; o servidor recusa com conflito.
- Um desligamento do daemon no meio de `ao_chegar` mata a extensão, e um arquivo meio movido fica para a extensão tratar.
