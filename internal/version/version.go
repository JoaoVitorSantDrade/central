// Package version guarda a versão do binário e do contrato HTTP.
package version

// Versao é a versão semântica do binário central. É var para a release
// gravá-la no build: -ldflags "-X github.com/JoaoVitorSantDrade/central/internal/version.Versao=1.2.3".
var Versao = "1.2.0"

// API é a versão do contrato HTTP público.
const API = "v1"
