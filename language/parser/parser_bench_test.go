package parser

import (
	"fmt"
	"strings"
	"testing"

	"github.com/IodeSystems/graphql-go/v2/language/source"
)

func buildWideQuery(nFields int) string {
	var sb strings.Builder
	sb.WriteString("query Wide {\n")
	for i := 0; i < nFields; i++ {
		fmt.Fprintf(&sb, "  field_%d\n", i)
	}
	sb.WriteString("}\n")
	return sb.String()
}

func buildDeepQuery(depth int) string {
	var sb strings.Builder
	sb.WriteString("query Deep {\n")
	for i := 0; i < depth; i++ {
		sb.WriteString(strings.Repeat("  ", i+1))
		fmt.Fprintf(&sb, "level_%d {\n", i)
	}
	sb.WriteString(strings.Repeat("  ", depth+1))
	sb.WriteString("leaf\n")
	for i := depth - 1; i >= 0; i-- {
		sb.WriteString(strings.Repeat("  ", i+1))
		sb.WriteString("}\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}

func buildArgsQuery(nFields int) string {
	var sb strings.Builder
	sb.WriteString("query Args($a: Int = 1, $b: String = \"x\", $c: Boolean = true) {\n")
	for i := 0; i < nFields; i++ {
		fmt.Fprintf(&sb, "  field_%d(a: $a, b: $b, c: $c, d: %d, e: \"hello\") @include(if: $c)\n", i, i)
	}
	sb.WriteString("}\n")
	return sb.String()
}

const tinyQuery = `{ user(id: 42) { id name email posts { title } } }`

func benchParse(b *testing.B, body string) {
	src := source.NewSource(&source.Source{Body: []byte(body)})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := Parse(ParseParams{Source: src})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParse_Tiny(b *testing.B) { benchParse(b, tinyQuery) }

func BenchmarkParse_Wide_100(b *testing.B) { benchParse(b, buildWideQuery(100)) }
func BenchmarkParse_Wide_1K(b *testing.B)  { benchParse(b, buildWideQuery(1000)) }
func BenchmarkParse_Wide_10K(b *testing.B) { benchParse(b, buildWideQuery(10000)) }

func BenchmarkParse_Deep_10(b *testing.B)  { benchParse(b, buildDeepQuery(10)) }
func BenchmarkParse_Deep_100(b *testing.B) { benchParse(b, buildDeepQuery(100)) }

func BenchmarkParse_Args_100(b *testing.B) { benchParse(b, buildArgsQuery(100)) }
func BenchmarkParse_Args_1K(b *testing.B)  { benchParse(b, buildArgsQuery(1000)) }
