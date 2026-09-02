package ast

import (
	"github.com/IodeSystems/graphql-go/v2/language/kinds"
)

// Directive implements Node
type Directive struct {
	Kind      string
	Loc       *Location
	Name      *Name
	Arguments []*Argument
}

func NewDirective(dir *Directive) *Directive {
	if dir == nil {
		dir = &Directive{}
	}
	dir.Kind = kinds.Directive
	return dir
}

func (dir *Directive) GetKind() string {
	return dir.Kind
}

func (dir *Directive) GetLoc() *Location {
	return dir.Loc
}
