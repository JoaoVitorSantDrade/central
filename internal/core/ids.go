package core

import "regexp"

var projectIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ValidProjectID diz se id serve como identificador de projeto (e nome de arquivo).
func ValidProjectID(id string) bool { return projectIDPattern.MatchString(id) }
