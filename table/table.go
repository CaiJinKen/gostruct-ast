package table

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/printer"
	"go/token"
	"io"
	"reflect"
	"strconv"
	"strings"

	"CaiJinKen/gostruct-ast/handler"
)

type Config struct {
	UseGormTag bool
	UseJsonTag bool
	SortField  bool   // sort field
	PkgName    string // output file package name
}

func (c *Config) Build() *Table {
	table := newTable()
	table.Config = *c
	return table
}

type defaultValue struct {
	value string
	valid bool
}

type Field struct {
	table *Table

	Name          string
	RawName       string
	Default       defaultValue
	TypeName      string
	RawTypeName   string
	Comment       string
	AutoIncrement bool
	Unsigned      bool
	NotNull       bool

	Type *Type

	indexes []*Index
}

type Type struct {
	size        uint
	decimalSize uint
	name        string
}

func (t *Type) String() string {
	if t.name == "" {
		return "interface{}"
	}
	return t.name
}

type Table struct {
	Config

	Name         string
	RawName      string
	Fields       []*Field
	PrimaryKeys  []string
	Indexes      []*Index
	nameFiledMap map[string]*Field
	Imports      map[string]string
	Comment      string

	model *model
}

type Index struct {
	Fields  []string
	Type    IndexType
	RawName string
	Comment string
}

type IndexType int

const (
	_ IndexType = iota
	Normal
	Unique
)

func (t IndexType) Name() string {
	switch t {
	case Normal:
		return "index"
	case Unique:
		return "uniqueIndex"
	}
	return ""
}

func newTable() *Table {
	return &Table{
		Name:         "",
		RawName:      "",
		Fields:       make([]*Field, 0),
		Indexes:      make([]*Index, 0),
		PrimaryKeys:  make([]string, 0),
		nameFiledMap: make(map[string]*Field),
		Imports:      make(map[string]string),

		model: newModel(),
	}
}

func (f *Field) parseType() {
	if f.TypeName == "" {
		return
	}
	tp := &Type{}
	f.Type = tp
	typeNameSlice := strings.Split(f.TypeName, "(")
	f.TypeName = typeNameSlice[0]
	if len(typeNameSlice) > 1 {
		lengths := strings.Split(typeNameSlice[1], "")
		str := strings.Join(lengths[:len(lengths)-1], "")
		sizes := strings.Split(str, ",")

		size, _ := strconv.Atoi(sizes[0])
		tp.size = uint(size)
		if len(sizes) > 1 {
			size, _ = strconv.Atoi(sizes[1])
			tp.decimalSize = uint(size)
		}
	}

	f.getType()
}

func (f *Field) getType() {
	switch f.TypeName {
	case "tinyint":
		f.Type.name = reflect.Int.String()
		if f.Type.size == 1 {
			f.Type.name = reflect.Bool.String()
		}
	case "smallint":
		f.Type.name = reflect.Int16.String()
		if f.Unsigned {
			f.Type.name = reflect.Uint16.String()
		}
	case "int", "integer":
		f.Type.name = reflect.Int.String()
		if f.Unsigned {
			f.Type.name = reflect.Uint.String()
		}
	case "bigint":
		f.Type.name = reflect.Int64.String()
		if f.Unsigned {
			f.Type.name = reflect.Uint64.String()
		}
	case "decimal", "float":
		f.Type.name = reflect.Float64.String()
	case "char", "varchar", "text", "longtext":
		f.Type.name = reflect.String.String()
	case "date", "datetime", "timestamp", "time":
		f.Type.name = "time.Time"
		f.table.Imports[" "] = "time"
	case "json":
		f.Type.name = "interface{}"
	}
	return
}

func (t *Table) parseField(line []byte) {
	contents := bytes.Split(line, []byte{' '})
	if len(contents) < 2 {
		return
	}
	f := &Field{table: t}
	t.Fields = append(t.Fields, f)

	name := trim(contents[0])
	f.RawName = string(name)
	f.Name = string(title(name))
	f.TypeName = string(trim(contents[1]))
	f.RawTypeName = f.TypeName

	t.nameFiledMap[f.RawName] = f

	for i := 2; i < len(contents); {
		value := contents[i]
		switch string(value) {
		case "NOT":
			if string(contents[i+1]) == "NULL" {
				f.NotNull = true
			}
			i += 2
			continue

		case "DEFAULT":
			f.Default = defaultValue{
				value: string(bytes.Trim(contents[i+1], "'")),
				valid: true,
			}
			if len(contents[i+1]) == 2 {
				f.Default.value = "''"
			}

			i += 1
			continue

		case "COMMENT":
			f.Comment = string(bytes.Trim(contents[i+1], "'"))
			i += 2
			continue

		case "AUTO_INCREMENT":
			f.AutoIncrement = true
		case "unsigned":
			f.Unsigned = true
		}
		i++
	}
	f.parseType()
}

func (t *Table) parseComment(line []byte) {
	idx := bytes.Index(line, []byte("COMMENT"))
	if idx < 0 {
		return
	}
	line = line[idx:]
	contents := bytes.Split(line, []byte{' '})
	line = contents[0]
	contents = bytes.Split(line, []byte{'='})
	if len(contents) < 2 {
		return
	}
	line = contents[1]
	line = bytes.Trim(line, ";")
	t.Comment = string(bytes.Trim(line, "'"))
}

func (t *Table) parseKey(line []byte) {
	contents := bytes.Split(line, []byte{' '})
	if len(contents) < 3 {
		return
	}

	for i := 2; i < len(contents); i++ {
		content := contents[i]
		content = bytes.TrimSpace(content)
		content = bytes.TrimPrefix(content, []byte{'('})
		content = bytes.TrimSuffix(content, []byte{')'})
		content = bytes.TrimSuffix(content, []byte{','})
		content = bytes.Trim(content, "`")
		t.PrimaryKeys = append(t.PrimaryKeys, string(content))
	}
}

func (t *Table) parseUniqueIndex(line []byte) {
	contents := bytes.Split(line, []byte{' '})
	if len(contents) < 3 {
		return
	}
	idx := &Index{
		Fields:  nil,
		Type:    Unique,
		RawName: string(trim(contents[2])),
		Comment: "",
	}
	size := len(contents[3])
	fields := contents[3][1 : size-1]
	for _, v := range bytes.Split(fields, []byte{','}) {
		idx.Fields = append(idx.Fields, string(v[1:len(v)-1]))
	}
	for i := 4; i < len(contents); i++ {
		v := contents[i]
		if string(v) != "COMMENT" {
			continue
		}
		idx.Comment = string(contents[i+1])
	}
	for _, v := range idx.Fields {
		if filed, ok := t.nameFiledMap[v]; ok && filed != nil {
			filed.indexes = append(filed.indexes, idx)
		}
	}
}

func (t *Table) parseIndex(line []byte) {
	contents := bytes.Split(line, []byte{' '})
	if len(contents) < 2 {
		return
	}
	idx := &Index{
		Fields:  nil,
		Type:    Normal,
		RawName: string(trim(contents[1])),
		Comment: "",
	}
	size := len(contents[2])
	fields := contents[2][1 : size-1]
	for _, v := range bytes.Split(fields, []byte{','}) {
		idx.Fields = append(idx.Fields, string(v[1:len(v)-1]))
	}
	for i := 3; i < len(contents); i++ {
		v := contents[i]
		if string(v) != "COMMENT" {
			continue
		}
		idx.Comment = string(contents[i+1])
	}

	for _, v := range idx.Fields {
		if filed, ok := t.nameFiledMap[v]; ok && filed != nil {
			filed.indexes = append(filed.indexes, idx)
		}
	}
}

func (t *Table) Parse(data []byte) {
	if len(data) == 0 {
		return
	}

	var continueComment bool

	reader := bufio.NewReader(bytes.NewReader(data))
	for {
		line, _, err := reader.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			handler.PrintErrAndExit(err)
		}
		line = trimLine(line)

		if len(line) == 0 {
			continue
		}

		if bytes.HasPrefix(line, []byte("/*")) {
			continueComment = true
			continue
		}

		if bytes.HasPrefix(line, []byte("*/")) {
			continueComment = false
			continue
		}

		if bytes.HasPrefix(line, []byte("--")) || continueComment || bytes.HasPrefix(line, []byte("SET")) || bytes.HasPrefix(line, []byte("DROP")) {
			continue
		}

		if bytes.HasPrefix(line, []byte("CREATE TABLE")) {
			name := trim(bytes.Split(line, []byte{' '})[2])
			t.RawName = string(name)
			t.Name = string(title(name))
			continue
		}

		line = bytes.Trim(line, ",")
		switch line[0] {
		case ')':
			t.parseComment(line)
		case '`':
			t.parseField(line)
		case 'P':
			t.parseKey(line)
		case 'I', 'K':
			t.parseIndex(line)
		case 'U':
			t.parseUniqueIndex(line)
		}
	}
	return
}

func (t *Table) GenCode() (data []byte) {
	if t == nil || t.Name == "" {
		return
	}

	name := &ast.Ident{Name: t.Name}

	var commentGroup *ast.CommentGroup
	if t.Comment != "" {
		commentGroup = &ast.CommentGroup{
			List: []*ast.Comment{{Text: toComment(t.Name + " " + t.Comment)}},
		}
	}

	var paths []*ast.BasicLit
	impMap := make(map[string]bool)
	fieldList := &ast.FieldList{}
	for _, v := range t.Fields {
		var (
			tag            *ast.BasicLit
			fieldCommGroup *ast.CommentGroup
		)
		if v.Comment != "" {
			fieldCommGroup = &ast.CommentGroup{
				List: []*ast.Comment{{Text: toComment(v.Comment)}},
			}
		}

		tag = buildTag(v)
		field := &ast.Field{
			Names:   []*ast.Ident{{Name: v.Name}},
			Type:    &ast.Ident{Name: v.Type.name},
			Tag:     tag,
			Comment: fieldCommGroup,
		}

		fieldList.List = append(fieldList.List, field)

		// import
		if v.Type.name == "time.Time" && !impMap[v.Type.name] {
			imp := &ast.BasicLit{Kind: token.STRING, Value: "\"time\""}
			paths = append(paths, imp)
			impMap[v.Type.name] = true
		}
	}

	obj := &ast.Object{
		Kind: ast.Typ,
		Name: t.Name,
		Decl: &ast.TypeSpec{
			Name: name,
			Type: &ast.StructType{
				Fields: fieldList,
			},
		},
	}

	name.Obj = obj

	file := &ast.File{
		Name: &ast.Ident{Name: t.PkgName},
		Scope: &ast.Scope{
			Objects: map[string]*ast.Object{t.Name: obj},
		},
		Decls: []ast.Decl{},
	}

	// add import
	for _, path := range paths {
		file.Decls = append(file.Decls,
			&ast.GenDecl{
				Tok:   token.IMPORT,
				Specs: []ast.Spec{&ast.ImportSpec{Path: path}},
			})
		file.Imports = append(file.Imports, &ast.ImportSpec{Path: path})
	}

	// add var type
	file.Decls = append(file.Decls, &ast.GenDecl{
		Doc: commentGroup,
		Tok: token.TYPE,
		Specs: []ast.Spec{
			&ast.TypeSpec{
				Name: name,
				Type: &ast.StructType{Fields: fieldList},
			},
		},
	})

	// tableName func
	receiverName := strings.Split(t.RawName, "")[0]
	ident := &ast.Ident{
		Name: receiverName,
		// Obj:  reciverObj,
	}

	tIdent := &ast.Ident{
		Name: t.Name,
	}

	receiverTypeObj := &ast.Object{
		Name: t.Name,
		Decl: &ast.TypeSpec{
			Name: tIdent,
			Type: &ast.StructType{
				Fields: &ast.FieldList{
					List: fieldList.List,
				},
			},
		},
	}

	tIdent.Obj = receiverTypeObj

	receiverObj := &ast.Object{
		Name: receiverName,
		Decl: &ast.Field{
			Names: []*ast.Ident{ident},
			Type: &ast.StarExpr{
				X: &ast.Ident{
					Name: t.Name,
					Obj:  receiverTypeObj,
				},
			},
		},
	}

	ident.Obj = receiverObj

	// add func
	file.Decls = append(file.Decls, &ast.FuncDecl{
		Recv: &ast.FieldList{
			List: []*ast.Field{
				{
					Names: []*ast.Ident{ident},
					Type:  &ast.StarExpr{X: &ast.Ident{Name: t.Name, Obj: receiverTypeObj}},
				},
			},
		},
		Name: &ast.Ident{Name: "TableName"},
		Type: &ast.FuncType{
			Params:  &ast.FieldList{List: []*ast.Field{}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: &ast.Ident{Name: "string"}}}},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ReturnStmt{
					Results: []ast.Expr{&ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf(`"%s"`, t.RawName)}},
				},
			},
		},
	})

	var buf bytes.Buffer
	printer.Fprint(&buf, token.NewFileSet(), file)
	data, _ = format.Source(buf.Bytes())

	return
}

func toComment(str string) string {
	return fmt.Sprintf("// %s", str)
}

func buildTag(t *Field) (tag *ast.BasicLit) {
	if !t.table.Config.UseJsonTag && !t.table.Config.UseGormTag {
		return
	}

	tag = &ast.BasicLit{
		Kind:  token.STRING,
		Value: "",
	}

	var tags []string
	if t.table.Config.UseJsonTag {
		tags = append(tags, fmt.Sprintf(`json:"%s"`, t.RawName))
	}

	if t.table.Config.UseGormTag {
		var gormTags []string
		gormTags = append(gormTags, fmt.Sprintf("column:%s;type:%s", t.RawName, t.RawTypeName))
		if t.Default.valid {
			gormTags = append(gormTags, fmt.Sprintf("default:%s", t.Default.value))
		}

		for _, v := range t.table.PrimaryKeys {
			if v == t.RawName {
				gormTags = append(gormTags, "primaryKey")
			}
		}
		for _, index := range t.indexes {
			for k, v := range index.Fields {
				if v != t.RawName {
					continue
				}
				gormTags = append(gormTags, fmt.Sprintf("%s:%s,priority:%d", index.Type.Name(), index.RawName, k+1))
			}
		}
		tags = append(tags, fmt.Sprintf(`gorm:"%s"`, strings.Join(gormTags, ";")))
	}

	tag.Value = fmt.Sprintf("`%s`", strings.Join(tags, " "))

	return
}
