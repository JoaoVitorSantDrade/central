# Registro do M4b (extensões do LyricCut e migração)

Execução nativa (sem subagentes), plano em `docs/plans/m4b-lyriccut.md`.
- **Central:** [PR #7](https://github.com/JoaoVitorSantDrade/central/pull/7) (fatos na resposta `ext/v1`) e branch `m4b-nomes`.
- **CortesEditor:** [PR #11](https://github.com/JoaoVitorSantDrade/CortesEditor/pull/11) para `develop`, aberto **sem merge**. A troca do manual e a abertura da sprint com a Central são do humano.

## Critério do M4 (spec §12)
"Digimon e um vídeo-meta novo adicionados sem editar arquivo à mão":
- **Sintético**, pela API de upload do painel: um vídeo-meta e um conjunto soltos na pasta de entrada viraram um formulário pendente, depois arquivos movidos, entradas no catálogo e o `ext.exemplo-novo` acordando o PESQUISADOR (motivo `ext`).
- **Real:** o Digimon foi catalogado pela aba Exemplos do painel e o Cyberpunk pela extensão, ambos em `videos.json` desta branch.
- **Pendente:** um vídeo-meta novo real fica para o humano soltar quando tiver um. Ainda não existe nenhum novo.

## Medido
- **Brief do LyricCut por cargo:** de 1784 a 3030 tokens (o critério do M2 é < 15 mil), contra ~37 mil do manual com as lições.
- **`importar-locks` do `$LOCKS` real:** 843 mensagens, 16 reservas, 14 e2e, 5 vagas e 4 contadores, com zero erros.
- **Medidor `nvidia-smi`:** leitura real pelo `doctor`.

## Decisões tomadas
- **`fatos` na resposta `ext/v1`.** O campo é opcional e aditivo, sem versão nova do contrato. As extensões leem a Central pelas rotas GET `/v1`, sem pasta.
- **Nomes do projeto valem em mais lugares.** `[[leitura]].papeis`, `[brief.fatos]` e `[sprint.acordar]` passaram a aceitar o nome do papel no projeto (FIXER), como as regras já aceitavam. O `central.toml` do LyricCut falhou no `doctor` sem isso.
- **Cartões do projeto ganham o fim de ciclo.** Eles terminam com a instrução de como esperar, salvo quando já falam em `central esperar`. Como esperar é mecânica da Central, não do projeto.
- **O painel mostra formulário e visão juntos.** O formulário pedido pela ação da aba aparece acima da visão, porque a `listar` do `exemplos` devolve a galeria e o pedido de catalogar.
- **Python sem junction.** Os testes do LyricCut carregam os scripts pelo caminho e rodam com o Python do checkout principal; nenhuma junction foi criada (lição L-014).

## Pequenos adiados
- O preprocess do `goal_videos.py` sempre usa o checkout principal; numa raiz de teste ele é pulado (`LYRICCUT_GV_PREPROCESS=0`).
- O comentário "PROPOSTA" no topo do `nucleo.md` entra no brief (~60 tokens) até o humano aprovar.
