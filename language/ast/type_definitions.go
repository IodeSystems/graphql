package ast

import (
	"github.com/IodeSystems/graphql-go/v2/language/kinds"
)

// DescribableNode are nodes that have descriptions associated with them.
type DescribableNode interface {
	GetDescription() *StringValue
}

type TypeDefinition interface {
	DescribableNode
	GetOperation() string
	GetVariableDefinitions() []*VariableDefinition
	GetSelectionSet() *SelectionSet
	GetKind() string
	GetLoc() *Location
}

var _ TypeDefinition = (*ScalarDefinition)(nil)
var _ TypeDefinition = (*ObjectDefinition)(nil)
var _ TypeDefinition = (*InterfaceDefinition)(nil)
var _ TypeDefinition = (*UnionDefinition)(nil)
var _ TypeDefinition = (*EnumDefinition)(nil)
var _ TypeDefinition = (*InputObjectDefinition)(nil)

type TypeSystemDefinition interface {
	GetOperation() string
	GetVariableDefinitions() []*VariableDefinition
	GetSelectionSet() *SelectionSet
	GetKind() string
	GetLoc() *Location
}

var _ TypeSystemDefinition = (*SchemaDefinition)(nil)
var _ TypeSystemDefinition = (TypeDefinition)(nil)
var _ TypeSystemDefinition = (*TypeExtensionDefinition)(nil)
var _ TypeSystemDefinition = (*DirectiveDefinition)(nil)

// SchemaDefinition implements Node, Definition
type SchemaDefinition struct {
	Kind           string
	Loc            *Location
	Directives     []*Directive
	OperationTypes []*OperationTypeDefinition
}

func NewSchemaDefinition(def *SchemaDefinition) *SchemaDefinition {
	if def == nil {
		def = &SchemaDefinition{}
	}
	def.Kind = kinds.SchemaDefinition
	return def
}

func (def *SchemaDefinition) GetKind() string {
	return def.Kind
}

func (def *SchemaDefinition) GetLoc() *Location {
	return def.Loc
}

func (def *SchemaDefinition) GetVariableDefinitions() []*VariableDefinition {
	return []*VariableDefinition{}
}

func (def *SchemaDefinition) GetSelectionSet() *SelectionSet {
	return &SelectionSet{}
}

func (def *SchemaDefinition) GetOperation() string {
	return ""
}

// OperationTypeDefinition implements Node, Definition
type OperationTypeDefinition struct {
	Kind      string
	Loc       *Location
	Operation string
	Type      *Named
}

func NewOperationTypeDefinition(def *OperationTypeDefinition) *OperationTypeDefinition {
	if def == nil {
		def = &OperationTypeDefinition{}
	}
	def.Kind = kinds.OperationTypeDefinition
	return def
}

func (def *OperationTypeDefinition) GetKind() string {
	return def.Kind
}

func (def *OperationTypeDefinition) GetLoc() *Location {
	return def.Loc
}

// ScalarDefinition implements Node, Definition
type ScalarDefinition struct {
	Kind        string
	Loc         *Location
	Description *StringValue
	Name        *Name
	Directives  []*Directive
}

func NewScalarDefinition(def *ScalarDefinition) *ScalarDefinition {
	if def == nil {
		def = &ScalarDefinition{}
	}
	def.Kind = kinds.ScalarDefinition
	return def
}

func (def *ScalarDefinition) GetKind() string {
	return def.Kind
}

func (def *ScalarDefinition) GetLoc() *Location {
	return def.Loc
}

func (def *ScalarDefinition) GetName() *Name {
	return def.Name
}

func (def *ScalarDefinition) GetVariableDefinitions() []*VariableDefinition {
	return []*VariableDefinition{}
}

func (def *ScalarDefinition) GetSelectionSet() *SelectionSet {
	return &SelectionSet{}
}

func (def *ScalarDefinition) GetOperation() string {
	return ""
}

func (def *ScalarDefinition) GetDescription() *StringValue {
	return def.Description
}

// ObjectDefinition implements Node, Definition
type ObjectDefinition struct {
	Kind        string
	Loc         *Location
	Name        *Name
	Description *StringValue
	Interfaces  []*Named
	Directives  []*Directive
	Fields      []*FieldDefinition
}

func NewObjectDefinition(def *ObjectDefinition) *ObjectDefinition {
	if def == nil {
		def = &ObjectDefinition{}
	}
	def.Kind = kinds.ObjectDefinition
	return def
}

func (def *ObjectDefinition) GetKind() string {
	return def.Kind
}

func (def *ObjectDefinition) GetLoc() *Location {
	return def.Loc
}

func (def *ObjectDefinition) GetName() *Name {
	return def.Name
}

func (def *ObjectDefinition) GetVariableDefinitions() []*VariableDefinition {
	return []*VariableDefinition{}
}

func (def *ObjectDefinition) GetSelectionSet() *SelectionSet {
	return &SelectionSet{}
}

func (def *ObjectDefinition) GetOperation() string {
	return ""
}

func (def *ObjectDefinition) GetDescription() *StringValue {
	return def.Description
}

// FieldDefinition implements Node
type FieldDefinition struct {
	Kind        string
	Loc         *Location
	Name        *Name
	Description *StringValue
	Arguments   []*InputValueDefinition
	Type        Type
	Directives  []*Directive
}

func NewFieldDefinition(def *FieldDefinition) *FieldDefinition {
	if def == nil {
		def = &FieldDefinition{}
	}
	def.Kind = kinds.FieldDefinition
	return def
}

func (def *FieldDefinition) GetKind() string {
	return def.Kind
}

func (def *FieldDefinition) GetLoc() *Location {
	return def.Loc
}

func (def *FieldDefinition) GetDescription() *StringValue {
	return def.Description
}

// InputValueDefinition implements Node
type InputValueDefinition struct {
	Kind         string
	Loc          *Location
	Name         *Name
	Description  *StringValue
	Type         Type
	DefaultValue Value
	Directives   []*Directive
}

func NewInputValueDefinition(def *InputValueDefinition) *InputValueDefinition {
	if def == nil {
		def = &InputValueDefinition{}
	}
	def.Kind = kinds.InputValueDefinition
	return def
}

func (def *InputValueDefinition) GetKind() string {
	return def.Kind
}

func (def *InputValueDefinition) GetLoc() *Location {
	return def.Loc
}

func (def *InputValueDefinition) GetDescription() *StringValue {
	return def.Description
}

// InterfaceDefinition implements Node, Definition
type InterfaceDefinition struct {
	Kind        string
	Loc         *Location
	Name        *Name
	Description *StringValue
	Directives  []*Directive
	Fields      []*FieldDefinition
}

func NewInterfaceDefinition(def *InterfaceDefinition) *InterfaceDefinition {
	if def == nil {
		def = &InterfaceDefinition{}
	}
	def.Kind = kinds.InterfaceDefinition
	return def
}

func (def *InterfaceDefinition) GetKind() string {
	return def.Kind
}

func (def *InterfaceDefinition) GetLoc() *Location {
	return def.Loc
}

func (def *InterfaceDefinition) GetName() *Name {
	return def.Name
}

func (def *InterfaceDefinition) GetVariableDefinitions() []*VariableDefinition {
	return []*VariableDefinition{}
}

func (def *InterfaceDefinition) GetSelectionSet() *SelectionSet {
	return &SelectionSet{}
}

func (def *InterfaceDefinition) GetOperation() string {
	return ""
}

func (def *InterfaceDefinition) GetDescription() *StringValue {
	return def.Description
}

// UnionDefinition implements Node, Definition
type UnionDefinition struct {
	Kind        string
	Loc         *Location
	Name        *Name
	Description *StringValue
	Directives  []*Directive
	Types       []*Named
}

func NewUnionDefinition(def *UnionDefinition) *UnionDefinition {
	if def == nil {
		def = &UnionDefinition{}
	}
	def.Kind = kinds.UnionDefinition
	return def
}

func (def *UnionDefinition) GetKind() string {
	return def.Kind
}

func (def *UnionDefinition) GetLoc() *Location {
	return def.Loc
}

func (def *UnionDefinition) GetName() *Name {
	return def.Name
}

func (def *UnionDefinition) GetVariableDefinitions() []*VariableDefinition {
	return []*VariableDefinition{}
}

func (def *UnionDefinition) GetSelectionSet() *SelectionSet {
	return &SelectionSet{}
}

func (def *UnionDefinition) GetOperation() string {
	return ""
}

func (def *UnionDefinition) GetDescription() *StringValue {
	return def.Description
}

// EnumDefinition implements Node, Definition
type EnumDefinition struct {
	Kind        string
	Loc         *Location
	Name        *Name
	Description *StringValue
	Directives  []*Directive
	Values      []*EnumValueDefinition
}

func NewEnumDefinition(def *EnumDefinition) *EnumDefinition {
	if def == nil {
		def = &EnumDefinition{}
	}
	def.Kind = kinds.EnumDefinition
	return def
}

func (def *EnumDefinition) GetKind() string {
	return def.Kind
}

func (def *EnumDefinition) GetLoc() *Location {
	return def.Loc
}

func (def *EnumDefinition) GetName() *Name {
	return def.Name
}

func (def *EnumDefinition) GetVariableDefinitions() []*VariableDefinition {
	return []*VariableDefinition{}
}

func (def *EnumDefinition) GetSelectionSet() *SelectionSet {
	return &SelectionSet{}
}

func (def *EnumDefinition) GetOperation() string {
	return ""
}

func (def *EnumDefinition) GetDescription() *StringValue {
	return def.Description
}

// EnumValueDefinition implements Node, Definition
type EnumValueDefinition struct {
	Kind        string
	Loc         *Location
	Name        *Name
	Description *StringValue
	Directives  []*Directive
}

func NewEnumValueDefinition(def *EnumValueDefinition) *EnumValueDefinition {
	if def == nil {
		def = &EnumValueDefinition{}
	}
	def.Kind = kinds.EnumValueDefinition
	return def
}

func (def *EnumValueDefinition) GetKind() string {
	return def.Kind
}

func (def *EnumValueDefinition) GetLoc() *Location {
	return def.Loc
}

func (def *EnumValueDefinition) GetDescription() *StringValue {
	return def.Description
}

// InputObjectDefinition implements Node, Definition
type InputObjectDefinition struct {
	Kind        string
	Loc         *Location
	Name        *Name
	Description *StringValue
	Directives  []*Directive
	Fields      []*InputValueDefinition
}

func NewInputObjectDefinition(def *InputObjectDefinition) *InputObjectDefinition {
	if def == nil {
		def = &InputObjectDefinition{}
	}
	def.Kind = kinds.InputObjectDefinition
	return def
}

func (def *InputObjectDefinition) GetKind() string {
	return def.Kind
}

func (def *InputObjectDefinition) GetLoc() *Location {
	return def.Loc
}

func (def *InputObjectDefinition) GetName() *Name {
	return def.Name
}

func (def *InputObjectDefinition) GetVariableDefinitions() []*VariableDefinition {
	return []*VariableDefinition{}
}

func (def *InputObjectDefinition) GetSelectionSet() *SelectionSet {
	return &SelectionSet{}
}

func (def *InputObjectDefinition) GetOperation() string {
	return ""
}

func (def *InputObjectDefinition) GetDescription() *StringValue {
	return def.Description
}
