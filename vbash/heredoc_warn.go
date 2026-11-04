package vbash

type UnterminatedHeredoc struct {
	Wanted  string
	Line    int
	EndLine int
}
