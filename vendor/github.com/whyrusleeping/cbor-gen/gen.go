package typegen

import (
	"fmt"
	"io"
	"reflect"
	"strings"
	"text/template"
)

const MaxLength = 8192

const ByteArrayMaxLen = 2 << 20

func doTemplate(w io.Writer, info interface{}, templ string) error {
	t := template.Must(template.New("").
		Funcs(template.FuncMap{}).Parse(templ))

	return t.Execute(w, info)
}

func PrintHeaderAndUtilityMethods(w io.Writer, pkg string) error {
	data := struct {
		Package string
	}{pkg}
	return doTemplate(w, data, `// Code generated by github.com/whyrusleeping/cbor-gen. DO NOT EDIT.

package {{ .Package }}

import (
	"fmt"
	"io"
	cbg "github.com/whyrusleeping/cbor-gen"
	xerrors "golang.org/x/xerrors"
)


var _ = xerrors.Errorf

`)
}

type Field struct {
	Name    string
	Pointer bool
	Type    reflect.Type
	Pkg     string

	IterLabel string
}

func typeName(pkg string, t reflect.Type) string {
	switch t.Kind() {
	case reflect.Slice:
		return "[]" + typeName(pkg, t.Elem())
	case reflect.Ptr:
		return "*" + typeName(pkg, t.Elem())
	case reflect.Map:
		return "map[" + typeName(pkg, t.Key()) + "]" + typeName(pkg, t.Elem())
	default:
		return strings.TrimPrefix(t.String(), pkg+".")
	}
}

func (f Field) TypeName() string {
	return typeName(f.Pkg, f.Type)
}

type GenTypeInfo struct {
	Name   string
	Fields []Field
}

func nameIsExported(name string) bool {
	return strings.ToUpper(name[0:1]) == name[0:1]
}

func ParseTypeInfo(pkg string, i interface{}) (*GenTypeInfo, error) {
	t := reflect.TypeOf(i)

	out := GenTypeInfo{
		Name: t.Name(),
	}

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !nameIsExported(f.Name) {
			continue
		}

		ft := f.Type
		var pointer bool
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
			pointer = true
		}

		out.Fields = append(out.Fields, Field{
			Name:    f.Name,
			Pointer: pointer,
			Type:    ft,
			Pkg:     pkg,
		})
	}

	return &out, nil
}

func (gti GenTypeInfo) TupleHeader() []byte {
	return CborEncodeMajorType(MajArray, uint64(len(gti.Fields)))
}

func (gti GenTypeInfo) TupleHeaderAsByteString() string {
	h := gti.TupleHeader()
	s := "[]byte{"
	for _, b := range h {
		s += fmt.Sprintf("%d,", b)
	}
	s += "}"
	return s
}

func (gti GenTypeInfo) MapHeader() []byte {
	return CborEncodeMajorType(MajMap, uint64(len(gti.Fields)))
}

func (gti GenTypeInfo) MapHeaderAsByteString() string {
	h := gti.MapHeader()
	s := "[]byte{"
	for _, b := range h {
		s += fmt.Sprintf("%d,", b)
	}
	s += "}"
	return s
}

func emitCborMarshalStringField(w io.Writer, f Field) error {
	if f.Pointer {
		return fmt.Errorf("pointers to strings not supported")
	}

	return doTemplate(w, f, `
	if len({{ .Name }}) > cbg.MaxLength {
		return xerrors.Errorf("Value in field {{ .Name | js }} was too long")
	}

	if _, err := w.Write(cbg.CborEncodeMajorType(cbg.MajTextString, uint64(len({{ .Name }})))); err != nil {
		return err
	}
	if _, err := w.Write([]byte({{ .Name }})); err != nil {
		return err
	}
`)
}
func emitCborMarshalStructField(w io.Writer, f Field) error {
	fname := f.Type.PkgPath() + "." + f.Type.Name()
	switch fname {
	case "math/big.Int":
		return doTemplate(w, f, `
	{
		if err := cbg.CborWriteHeader(w, cbg.MajTag, 2); err != nil {
			return err
		}
		var b []byte
		if {{ .Name }} != nil {
			b = {{ .Name }}.Bytes()
		}

		if err := cbg.CborWriteHeader(w, cbg.MajByteString, uint64(len(b))); err != nil {
			return err
		}
		if _, err := w.Write(b); err != nil {
			return err
		}
	}
`)

	case "github.com/ipfs/go-cid.Cid":
		return doTemplate(w, f, `
{{ if .Pointer }}
	if {{ .Name }} == nil {
		if _, err := w.Write(cbg.CborNull); err != nil {
			return err
		}
	} else {
		if err := cbg.WriteCid(w, *{{ .Name }}); err != nil {
			return xerrors.Errorf("failed to write cid field {{ .Name }}: %w", err)
		}
	}
{{ else }}
	if err := cbg.WriteCid(w, {{ .Name }}); err != nil {
		return xerrors.Errorf("failed to write cid field {{ .Name }}: %w", err)
	}
{{ end }}
`)
	default:
		return doTemplate(w, f, `
	if err := {{ .Name }}.MarshalCBOR(w); err != nil {
		return err
	}
`)
	}

}

func emitCborMarshalUint64Field(w io.Writer, f Field) error {
	if f.Pointer {
		return fmt.Errorf("pointers to integers not supported")
	}
	return doTemplate(w, f, `
	if _, err := w.Write(cbg.CborEncodeMajorType(cbg.MajUnsignedInt, uint64({{ .Name }}))); err != nil {
		return err
	}
`)
}

func emitCborMarshalUint8Field(w io.Writer, f Field) error {
	if f.Pointer {
		return fmt.Errorf("pointers to integers not supported")
	}
	return doTemplate(w, f, `
	if _, err := w.Write(cbg.CborEncodeMajorType(cbg.MajUnsignedInt, uint64({{ .Name }}))); err != nil {
		return err
	}
`)
}

func emitCborMarshalInt64Field(w io.Writer, f Field) error {
	if f.Pointer {
		return fmt.Errorf("pointers to integers not supported")
	}

	// if negative
	// val = -1 - cbor
	// cbor = -val -1

	return doTemplate(w, f, `
	if {{ .Name }} >= 0 {
	    if _, err := w.Write(cbg.CborEncodeMajorType(cbg.MajUnsignedInt, uint64({{ .Name }}))); err != nil {
		    return err
	    }
	} else {
	    if _, err := w.Write(cbg.CborEncodeMajorType(cbg.MajNegativeInt, uint64(-{{ .Name }})-1)); err != nil {
		    return err
	    }
	}
`)
}

func emitCborMarshalBoolField(w io.Writer, f Field) error {
	return doTemplate(w, f, `
	if err := cbg.WriteBool(w, {{ .Name }}); err != nil {
		return err
	}
`)
}

func emitCborMarshalMapField(w io.Writer, f Field) error {
	err := doTemplate(w, f, `
{
	if len({{ .Name }}) > 4096 {
		return xerrors.Errorf("cannot marshal {{ .Name }} map too large")
	}

	if err := cbg.CborWriteHeader(w, cbg.MajMap, uint64(len({{ .Name }}))); err != nil {
		return err
	}

	keys := make([]string, 0, len({{ .Name }}))
	for k := range {{ .Name }} {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := {{ .Name }}[k]

`)
	if err != nil {
		return err
	}

	// Map key
	switch f.Type.Key().Kind() {
	case reflect.String:
		if err := emitCborMarshalStringField(w, Field{Name: "k"}); err != nil {
			return err
		}
	default:
		return fmt.Errorf("non-string map keys are not yet supported")
	}

	// Map value
	switch f.Type.Elem().Kind() {
	case reflect.Ptr:
		if f.Type.Elem().Elem().Kind() != reflect.Struct {
			return fmt.Errorf("unsupported map elem ptr type: %s", f.Type.Elem())
		}

		fallthrough
	case reflect.Struct:
		if err := emitCborMarshalStructField(w, Field{Name: "v", Type: f.Type.Elem(), Pkg: f.Pkg}); err != nil {
			return err
		}
	default:
		return fmt.Errorf("currently unsupported map elem type: %s", f.Type.Elem())
	}

	return doTemplate(w, f, `
	}
	}
`)
}

func emitCborMarshalSliceField(w io.Writer, f Field) error {
	if f.Pointer {
		return fmt.Errorf("pointers to slices not supported")
	}
	e := f.Type.Elem()

	if e.Kind() == reflect.Uint8 {
		return doTemplate(w, f, `
	if len({{ .Name }}) > cbg.ByteArrayMaxLen {
		return xerrors.Errorf("Byte array in field {{ .Name }} was too long")
	}

	if _, err := w.Write(cbg.CborEncodeMajorType(cbg.MajByteString, uint64(len({{ .Name }})))); err != nil {
		return err
	}
	if _, err := w.Write({{ .Name }}); err != nil {
		return err
	}
`)
	}

	if e.Kind() == reflect.Ptr {
		e = e.Elem()
	}

	err := doTemplate(w, f, `
	if len({{ .Name }}) > cbg.MaxLength {
		return xerrors.Errorf("Slice value in field {{ .Name }} was too long")
	}

	if _, err := w.Write(cbg.CborEncodeMajorType(cbg.MajArray, uint64(len({{ .Name }})))); err != nil {
		return err
	}
	for _, v := range {{ .Name }} {`)
	if err != nil {
		return err
	}

	switch e.Kind() {
	default:
		return fmt.Errorf("do not yet support slices of %s yet", e.Kind())
	case reflect.Struct:
		fname := e.PkgPath() + "." + e.Name()
		switch fname {
		case "github.com/ipfs/go-cid.Cid":
			err := doTemplate(w, f, `
		if err := cbg.WriteCid(w, v); err != nil {
			return xerrors.Errorf("failed writing cid field {{ .Name }}: %w", err)
		}
`)
			if err != nil {
				return err
			}

		default:
			err := doTemplate(w, f, `
		if err := v.MarshalCBOR(w); err != nil {
			return err
		}
`)
			if err != nil {
				return err
			}
		}
	case reflect.Uint64:
		err := doTemplate(w, f, `
		if err := cbg.CborWriteHeader(w, cbg.MajUnsignedInt, v); err != nil {
			return err
		}
`)
		if err != nil {
			return err
		}
	case reflect.Uint8:
		err := doTemplate(w, f, `
		if err := cbg.CborWriteHeader(w, cbg.MajUnsignedInt, uint64(v)); err != nil {
			return err
		}
`)
		if err != nil {
			return err
		}
	case reflect.Int64:
		subf := Field{Name: "v", Type: e, Pkg: f.Pkg}
		if err := emitCborMarshalInt64Field(w, subf); err != nil {
			return err
		}

	case reflect.Slice:
		subf := Field{Name: "v", Type: e, Pkg: f.Pkg}
		if err := emitCborMarshalSliceField(w, subf); err != nil {
			return err
		}
	}

	// array end
	fmt.Fprintf(w, "\t}\n")
	return nil
}

func emitCborMarshalStructTuple(w io.Writer, gti *GenTypeInfo) error {
	err := doTemplate(w, gti, `func (t *{{ .Name }}) MarshalCBOR(w io.Writer) error {
	if t == nil {
		_, err := w.Write(cbg.CborNull)
		return err
	}
	if _, err := w.Write({{ .TupleHeaderAsByteString }}); err != nil {
		return err
	}
`)
	if err != nil {
		return err
	}

	for _, f := range gti.Fields {
		fmt.Fprintf(w, "\n\t// t.%s (%s) (%s)", f.Name, f.Type, f.Type.Kind())
		f.Name = "t." + f.Name

		switch f.Type.Kind() {
		case reflect.String:
			if err := emitCborMarshalStringField(w, f); err != nil {
				return err
			}
		case reflect.Struct:
			if err := emitCborMarshalStructField(w, f); err != nil {
				return err
			}
		case reflect.Uint64:
			if err := emitCborMarshalUint64Field(w, f); err != nil {
				return err
			}
		case reflect.Uint8:
			if err := emitCborMarshalUint8Field(w, f); err != nil {
				return err
			}
		case reflect.Int64:
			if err := emitCborMarshalInt64Field(w, f); err != nil {
				return err
			}
		case reflect.Slice:
			if err := emitCborMarshalSliceField(w, f); err != nil {
				return err
			}
		case reflect.Bool:
			if err := emitCborMarshalBoolField(w, f); err != nil {
				return err
			}
		case reflect.Map:
			if err := emitCborMarshalMapField(w, f); err != nil {
				return err
			}
		default:
			return fmt.Errorf("field %q of %q has unsupported kind %q", f.Name, gti.Name, f.Type.Kind())
		}
	}

	fmt.Fprintf(w, "\treturn nil\n}\n\n")
	return nil
}

func emitCborUnmarshalStringField(w io.Writer, f Field) error {
	if f.Pointer {
		return fmt.Errorf("pointers to strings not supported")
	}
	if f.Type == nil {
		f.Type = reflect.TypeOf("")
	}
	return doTemplate(w, f, `
	{
		sval, err := cbg.ReadString(br)
		if err != nil {
			return err
		}

		{{ .Name }} = {{ .TypeName }}(sval)
	}
`)
}

func emitCborUnmarshalStructField(w io.Writer, f Field) error {
	fname := f.Type.PkgPath() + "." + f.Type.Name()

	switch fname {
	case "math/big.Int":
		return doTemplate(w, f, `
	maj, extra, err = cbg.CborReadHeader(br)
	if err != nil {
		return err
	}

	if maj != cbg.MajTag || extra != 2 {
		return fmt.Errorf("big ints should be cbor bignums")
	}

	maj, extra, err = cbg.CborReadHeader(br)
	if err != nil {
		return err
	}

	if maj != cbg.MajByteString {
		return fmt.Errorf("big ints should be tagged cbor byte strings")
	}

	if extra > 256 {
		return fmt.Errorf("{{ .Name }}: cbor bignum was too large")
	}

	if extra > 0 {
		buf := make([]byte, extra)
		if _, err := io.ReadFull(br, buf); err != nil {
			return err
		}
		{{ .Name }} = big.NewInt(0).SetBytes(buf)
	} else {
		{{ .Name }} = big.NewInt(0)
	}
`)
	case "github.com/ipfs/go-cid.Cid":
		return doTemplate(w, f, `
	{
{{ if .Pointer }}
		pb, err := br.PeekByte()
		if err != nil {
			return err
		}
		if pb == cbg.CborNull[0] {
			var nbuf [1]byte
			if _, err := br.Read(nbuf[:]); err != nil {
				return err
			}
		} else {
{{ end }}
		c, err := cbg.ReadCid(br)
		if err != nil {
			return xerrors.Errorf("failed to read cid field {{ .Name }}: %w", err)
		}
{{ if .Pointer }}
			{{ .Name }} = &c
		}
{{ else }}
		{{ .Name }} = c
{{ end }}
	}
`)
	default:
		return doTemplate(w, f, `
	{
{{ if .Pointer }}
		pb, err := br.PeekByte()
		if err != nil {
			return err
		}
		if pb == cbg.CborNull[0] {
			var nbuf [1]byte
			if _, err := br.Read(nbuf[:]); err != nil {
				return err
			}
		} else {
			{{ .Name }} = new({{ .TypeName }})
			if err := {{ .Name }}.UnmarshalCBOR(br); err != nil {
				return err
			}
		}
{{ else }}
		if err := {{ .Name }}.UnmarshalCBOR(br); err != nil {
			return err
		}
{{ end }}
	}
`)
	}
}

func emitCborUnmarshalInt64Field(w io.Writer, f Field) error {
	return doTemplate(w, f, `{
	maj, extra, err := cbg.CborReadHeader(br)
	var extraI int64
	if err != nil {
		return err
	}
	switch maj {
	case cbg.MajUnsignedInt:
		extraI = int64(extra)
		if extraI < 0 {
			return fmt.Errorf("int64 positive overflow")
	   }
	case cbg.MajNegativeInt:
		extraI = int64(extra)
		if extraI < 0 {
			return fmt.Errorf("int64 negative oveflow")
		}
		extraI = -1 - extraI
	default:
		return fmt.Errorf("wrong type for int64 field: %d", maj)
	}

	{{ .Name }} = {{ .TypeName }}(extraI)
}
`)
}

func emitCborUnmarshalUint64Field(w io.Writer, f Field) error {
	return doTemplate(w, f, `
	maj, extra, err = cbg.CborReadHeader(br)
	if err != nil {
		return err
	}
	if maj != cbg.MajUnsignedInt {
		return fmt.Errorf("wrong type for uint64 field")
	}
	{{ .Name }} = {{ .TypeName }}(extra)
`)
}

func emitCborUnmarshalUint8Field(w io.Writer, f Field) error {
	return doTemplate(w, f, `
	maj, extra, err = cbg.CborReadHeader(br)
	if err != nil {
		return err
	}
	if maj != cbg.MajUnsignedInt {
		return fmt.Errorf("wrong type for uint8 field")
	}
	if extra > math.MaxUint8 {
		return fmt.Errorf("integer in input was too large for uint8 field")
	}
	{{ .Name }} = {{ .TypeName }}(extra)
`)
}

func emitCborUnmarshalBoolField(w io.Writer, f Field) error {
	return doTemplate(w, f, `
	maj, extra, err = cbg.CborReadHeader(br)
	if err != nil {
		return err
	}
	if maj != cbg.MajOther {
		return fmt.Errorf("booleans must be major type 7")
	}
	switch extra {
	case 20:
		{{ .Name }} = false
	case 21:
		{{ .Name }} = true
	default:
		return fmt.Errorf("booleans are either major type 7, value 20 or 21 (got %d)", extra)
	}
`)
}

func emitCborUnmarshalMapField(w io.Writer, f Field) error {
	err := doTemplate(w, f, `
	maj, extra, err = cbg.CborReadHeader(br)
	if err != nil {
		return err
	}
	if maj != cbg.MajMap {
		return fmt.Errorf("expected a map (major type 5)")
	}
	if extra > 4096 {
		return fmt.Errorf("{{ .Name }}: map too large")
	}

	{{ .Name }} = make({{ .TypeName }}, extra)


	for i, l := 0, int(extra); i < l; i++ {
`)
	if err != nil {
		return err
	}

	switch f.Type.Key().Kind() {
	case reflect.String:
		if err := doTemplate(w, f, `
	var k string
`); err != nil {
			return err
		}
		if err := emitCborUnmarshalStringField(w, Field{Name: "k"}); err != nil {
			return err
		}
	default:
		return fmt.Errorf("maps with non-string keys are not yet supported")
	}

	var pointer bool
	t := f.Type.Elem()
	switch t.Kind() {
	case reflect.Ptr:
		if t.Elem().Kind() != reflect.Struct {
			return fmt.Errorf("unsupported map elem ptr type: %s", t)
		}

		pointer = true
		fallthrough
	case reflect.Struct:
		subf := Field{Name: "v", Pointer: pointer, Type: t, Pkg: f.Pkg}
		if err := doTemplate(w, subf, `
	var v {{ .TypeName }}
`); err != nil {
			return err
		}

		if pointer {
			subf.Type = subf.Type.Elem()
		}
		if err := emitCborUnmarshalStructField(w, subf); err != nil {
			return err
		}
		if err := doTemplate(w, f, `
	{{ .Name }}[k] = v
`); err != nil {
			return err
		}
	default:
		return fmt.Errorf("currently only support maps of structs")
	}

	return doTemplate(w, f, `
	}
`)
}

func emitCborUnmarshalSliceField(w io.Writer, f Field) error {
	if f.IterLabel == "" {
		f.IterLabel = "i"
	}

	e := f.Type.Elem()
	var pointer bool
	if e.Kind() == reflect.Ptr {
		pointer = true
		e = e.Elem()
	}

	err := doTemplate(w, f, `
	maj, extra, err = cbg.CborReadHeader(br)
	if err != nil {
		return err
	}
`)
	if err != nil {
		return err
	}

	if e.Kind() == reflect.Uint8 {
		return doTemplate(w, f, `
	if extra > cbg.ByteArrayMaxLen {
		return fmt.Errorf("{{ .Name }}: byte array too large (%d)", extra)
	}
	if maj != cbg.MajByteString {
		return fmt.Errorf("expected byte array")
	}
	{{ .Name }} = make([]byte, extra)
	if _, err := io.ReadFull(br, {{ .Name }}); err != nil {
		return err
	}
`)
	}

	if err := doTemplate(w, f, `
	if extra > cbg.MaxLength {
		return fmt.Errorf("{{ .Name }}: array too large (%d)", extra)
	}
`); err != nil {
		return err
	}

	err = doTemplate(w, f, `
	if maj != cbg.MajArray {
		return fmt.Errorf("expected cbor array")
	}
	if extra > 0 {
		{{ .Name }} = make({{ .TypeName }}, extra)
	}
	for {{ .IterLabel }} := 0; {{ .IterLabel }} < int(extra); {{ .IterLabel }}++ {
`)
	if err != nil {
		return err
	}

	switch e.Kind() {
	case reflect.Struct:
		fname := e.PkgPath() + "." + e.Name()
		switch fname {
		case "github.com/ipfs/go-cid.Cid":
			err := doTemplate(w, f, `
		c, err := cbg.ReadCid(br)
		if err != nil {
			return xerrors.Errorf("reading cid field {{ .Name }} failed: %w", err)
		}
		{{ .Name }}[{{ .IterLabel }}] = c
`)
			if err != nil {
				return err
			}
		default:
			subf := Field{
				Type:    e,
				Pkg:     f.Pkg,
				Pointer: pointer,
				Name:    f.Name + "[" + f.IterLabel + "]",
			}

			err := doTemplate(w, subf, `
		var v {{ .TypeName }}
		if err := v.UnmarshalCBOR(br); err != nil {
			return err
		}

		{{ .Name }} = {{ if .Pointer }}&{{ end }}v
`)
			if err != nil {
				return err
			}
		}
	case reflect.Uint64:
		err := doTemplate(w, f, `
		maj, val, err := cbg.CborReadHeader(br)
		if err != nil {
			return xerrors.Errorf("failed to read uint64 for {{ .Name }} slice: %w", err)
		}

		if maj != cbg.MajUnsignedInt {
			return xerrors.Errorf("value read for array {{ .Name }} was not a uint, instead got %d", maj)
		}
		
		{{ .Name }}[{{ .IterLabel}}] = val
`)
		if err != nil {
			return err
		}
	case reflect.Int64:
		subf := Field{
			Type: e,
			Pkg:  f.Pkg,
			Name: f.Name + "[" + f.IterLabel + "]",
		}
		err := emitCborUnmarshalInt64Field(w, subf)
		if err != nil {
			return err
		}
	case reflect.Slice:
		nextIter := string([]byte{f.IterLabel[0] + 1})
		subf := Field{
			Name:      fmt.Sprintf("%s[%s]", f.Name, f.IterLabel),
			Type:      e,
			IterLabel: nextIter,
			Pkg:       f.Pkg,
		}
		fmt.Fprintf(w, "\t\t{\n\t\t\tvar maj byte\n\t\tvar extra uint64\n\t\tvar err error\n")
		if err := emitCborUnmarshalSliceField(w, subf); err != nil {
			return err
		}
		fmt.Fprintf(w, "\t\t}\n")

	default:
		return fmt.Errorf("do not yet support slices of %s yet", e.Elem())
	}
	fmt.Fprintf(w, "\t}\n\n")

	return nil
}

func emitCborUnmarshalStructTuple(w io.Writer, gti *GenTypeInfo) error {
	err := doTemplate(w, gti, `
func (t *{{ .Name}}) UnmarshalCBOR(r io.Reader) error {
	br := cbg.GetPeeker(r)

	maj, extra, err := cbg.CborReadHeader(br)
	if err != nil {
		return err
	}
	if maj != cbg.MajArray {
		return fmt.Errorf("cbor input should be of type array")
	}

	if extra != {{ len .Fields }} {
		return fmt.Errorf("cbor input had wrong number of fields")
	}

`)
	if err != nil {
		return err
	}

	for _, f := range gti.Fields {
		fmt.Fprintf(w, "\t// t.%s (%s) (%s)\n", f.Name, f.Type, f.Type.Kind())
		f.Name = "t." + f.Name

		switch f.Type.Kind() {
		case reflect.String:
			if err := emitCborUnmarshalStringField(w, f); err != nil {
				return err
			}
		case reflect.Struct:
			if err := emitCborUnmarshalStructField(w, f); err != nil {
				return err
			}
		case reflect.Uint64:
			if err := emitCborUnmarshalUint64Field(w, f); err != nil {
				return err
			}
		case reflect.Uint8:
			if err := emitCborUnmarshalUint8Field(w, f); err != nil {
				return err
			}
		case reflect.Int64:
			if err := emitCborUnmarshalInt64Field(w, f); err != nil {
				return err
			}
		case reflect.Slice:
			if err := emitCborUnmarshalSliceField(w, f); err != nil {
				return err
			}
		case reflect.Bool:
			if err := emitCborUnmarshalBoolField(w, f); err != nil {
				return err
			}
		case reflect.Map:
			if err := emitCborUnmarshalMapField(w, f); err != nil {
				return err
			}
		default:
			return fmt.Errorf("field %q of %q has unsupported kind %q", f.Name, gti.Name, f.Type.Kind())
		}
	}

	fmt.Fprintf(w, "\treturn nil\n}\n\n")

	return nil
}

// Generates 'tuple representation' cbor encoders for the given type
func GenTupleEncodersForType(inpkg string, i interface{}, w io.Writer) error {
	gti, err := ParseTypeInfo(inpkg, i)
	if err != nil {
		return err
	}

	if err := emitCborMarshalStructTuple(w, gti); err != nil {
		return err
	}

	if err := emitCborUnmarshalStructTuple(w, gti); err != nil {
		return err
	}

	return nil
}

func emitCborMarshalStructMap(w io.Writer, gti *GenTypeInfo) error {
	err := doTemplate(w, gti, `func (t *{{ .Name }}) MarshalCBOR(w io.Writer) error {
	if t == nil {
		_, err := w.Write(cbg.CborNull)
		return err
	}
	if _, err := w.Write({{ .MapHeaderAsByteString }}); err != nil {
		return err
	}
`)
	if err != nil {
		return err
	}

	for _, f := range gti.Fields {
		fmt.Fprintf(w, "\n\t// t.%s (%s) (%s)", f.Name, f.Type, f.Type.Kind())

		if err := emitCborMarshalStringField(w, Field{
			Name: `"` + f.Name + `"`,
		}); err != nil {
			return err
		}

		f.Name = "t." + f.Name

		switch f.Type.Kind() {
		case reflect.String:
			if err := emitCborMarshalStringField(w, f); err != nil {
				return err
			}
		case reflect.Struct:
			if err := emitCborMarshalStructField(w, f); err != nil {
				return err
			}
		case reflect.Uint64:
			if err := emitCborMarshalUint64Field(w, f); err != nil {
				return err
			}
		case reflect.Uint8:
			if err := emitCborMarshalUint8Field(w, f); err != nil {
				return err
			}
		case reflect.Slice:
			if err := emitCborMarshalSliceField(w, f); err != nil {
				return err
			}
		case reflect.Bool:
			if err := emitCborMarshalBoolField(w, f); err != nil {
				return err
			}
		case reflect.Map:
			if err := emitCborMarshalMapField(w, f); err != nil {
				return err
			}
		default:
			return fmt.Errorf("field %q of %q has unsupported kind %q", f.Name, gti.Name, f.Type.Kind())
		}
	}

	fmt.Fprintf(w, "\treturn nil\n}\n\n")
	return nil
}

func emitCborUnmarshalStructMap(w io.Writer, gti *GenTypeInfo) error {
	err := doTemplate(w, gti, `
func (t *{{ .Name}}) UnmarshalCBOR(r io.Reader) error {
	br := cbg.GetPeeker(r)

	maj, extra, err := cbg.CborReadHeader(br)
	if err != nil {
		return err
	}
	if maj != cbg.MajMap {
		return fmt.Errorf("cbor input should be of type map")
	}

	if extra > cbg.MaxLength {
		return fmt.Errorf("{{ .Name }}: map struct too large (%d)", extra)
	}

	var name string
	n := extra

	for i := uint64(0); i < n; i++ {
`)
	if err != nil {
		return err
	}

	if err := emitCborUnmarshalStringField(w, Field{Name: "name"}); err != nil {
		return err
	}

	err = doTemplate(w, gti, `
		switch name {
`)
	if err != nil {
		return err
	}

	for _, f := range gti.Fields {
		fmt.Fprintf(w, "// t.%s (%s) (%s)", f.Name, f.Type, f.Type.Kind())

		err := doTemplate(w, f, `
		case "{{ .Name }}":
`)
		if err != nil {
			return err
		}

		f.Name = "t." + f.Name

		switch f.Type.Kind() {
		case reflect.String:
			if err := emitCborUnmarshalStringField(w, f); err != nil {
				return err
			}
		case reflect.Struct:
			if err := emitCborUnmarshalStructField(w, f); err != nil {
				return err
			}
		case reflect.Uint64:
			if err := emitCborUnmarshalUint64Field(w, f); err != nil {
				return err
			}
		case reflect.Uint8:
			if err := emitCborUnmarshalUint8Field(w, f); err != nil {
				return err
			}
		case reflect.Slice:
			if err := emitCborUnmarshalSliceField(w, f); err != nil {
				return err
			}
		case reflect.Bool:
			if err := emitCborUnmarshalBoolField(w, f); err != nil {
				return err
			}
		case reflect.Map:
			if err := emitCborUnmarshalMapField(w, f); err != nil {
				return err
			}
		default:
			return fmt.Errorf("field %q of %q has unsupported kind %q", f.Name, gti.Name, f.Type.Kind())
		}
	}

	return doTemplate(w, gti, `
		default:
			return fmt.Errorf("unknown struct field %d: '%s'", i, name)
		}
	}

	return nil
}
`)
}

// Generates 'tuple representation' cbor encoders for the given type
func GenMapEncodersForType(inpkg string, i interface{}, w io.Writer) error {
	gti, err := ParseTypeInfo(inpkg, i)
	if err != nil {
		return err
	}

	if err := emitCborMarshalStructMap(w, gti); err != nil {
		return err
	}

	if err := emitCborUnmarshalStructMap(w, gti); err != nil {
		return err
	}

	return nil
}