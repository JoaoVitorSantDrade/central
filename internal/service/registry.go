package service

// registryMigrations é o esquema do registro da máquina (registro.db).
var registryMigrations = []string{
	`CREATE TABLE projetos (
		id            TEXT PRIMARY KEY,
		nome          TEXT NOT NULL,
		raiz          TEXT NOT NULL,
		ultimo_acesso TEXT NOT NULL
	);`,
}
