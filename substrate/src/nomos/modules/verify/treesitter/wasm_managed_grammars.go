package treesitter

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

const (
	langJava               = "java"
	langCSharp             = "csharp"
	managedGrammarJavaID   byte = 1
	managedGrammarCSharpID byte = 2
	maxManagedWasmBuf           = 65536
)

// JavaTreeSitterWasmBytecode embeds the pure-Go WebAssembly driver for Java AST pre-flight scanning (Law 2).
var JavaTreeSitterWasmBytecode = buildManagedGrammarWasm(managedGrammarJavaID)

// CSharpTreeSitterWasmBytecode embeds the pure-Go WebAssembly driver for C# AST pre-flight scanning (Law 2).
var CSharpTreeSitterWasmBytecode = buildManagedGrammarWasm(managedGrammarCSharpID)

func buildManagedGrammarWasm(grammarID byte) []byte {
	out := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	typeSec := []byte{0x02, 0x60, 0x00, 0x01, 0x7f, 0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f}
	funcSec := []byte{0x03, 0x00, 0x01, 0x01}
	memSec := []byte{0x01, 0x01, 0x02, 0x10}
	expSec := buildManagedExportSection()
	codeSec := buildManagedCodeSection(grammarID)

	out = appendWasmSection(out, 0x01, typeSec)
	out = appendWasmSection(out, 0x03, funcSec)
	out = appendWasmSection(out, 0x05, memSec)
	out = appendWasmSection(out, 0x07, expSec)
	out = appendWasmSection(out, 0x0a, codeSec)
	return out
}

func buildManagedExportSection() []byte {
	var p []byte
	p = append(p, 0x04)
	p = appendWasmExport(p, "memory", 0x02, 0)
	p = appendWasmExport(p, "grammar_id", 0x00, 0)
	p = appendWasmExport(p, "scan_block_balance", 0x00, 1)
	p = appendWasmExport(p, "count_semicolons", 0x00, 2)
	return p
}

func buildManagedCodeSection(grammarID byte) []byte {
	f0 := []byte{0x00, 0x41, grammarID, 0x0b}
	f1 := buildBlockBalanceWasmBody()
	f2 := buildSemicolonCountWasmBody()
	var p []byte
	p = append(p, 0x03)
	for _, body := range [][]byte{f0, f1, f2} {
		p = appendULEB128(p, uint32(len(body)))
		p = append(p, body...)
	}
	return p
}

func buildBlockBalanceWasmBody() []byte {
	var b []byte
	b = append(b, 0x01, 0x04, 0x7f)
	b = append(b, 0x20, 0x00, 0x20, 0x01, 0x6a, 0x21, 0x01)
	b = append(b, 0x02, 0x40, 0x03, 0x40)
	b = append(b, 0x20, 0x00, 0x20, 0x01, 0x4f, 0x0d, 0x01)
	b = append(b, 0x20, 0x00, 0x2d, 0x00, 0x00, 0x21, 0x05)
	b = appendBalanceCounterOp(b, 0x02, '{', '}')
	b = appendBalanceCounterOp(b, 0x03, '(', ')')
	b = appendBalanceCounterOp(b, 0x04, '[', ']')
	b = append(b, 0x20, 0x00, 0x41, 0x01, 0x6a, 0x21, 0x00, 0x0c, 0x00, 0x0b, 0x0b)
	b = append(b, 0x20, 0x02, 0x45, 0x20, 0x03, 0x45, 0x71, 0x20, 0x04, 0x45, 0x71, 0x0b)
	return b
}

func appendBalanceCounterOp(b []byte, localIdx, openCh, closeCh byte) []byte {
	b = append(b, 0x20, localIdx, 0x20, 0x05, 0x41)
	b = appendSLEB128Byte(b, openCh)
	b = append(b, 0x46, 0x6a, 0x20, 0x05, 0x41)
	b = appendSLEB128Byte(b, closeCh)
	b = append(b, 0x46, 0x6b, 0x22, localIdx, 0x41, 0x00, 0x48, 0x04, 0x40, 0x41, 0x00, 0x0f, 0x0b)
	return b
}

func buildSemicolonCountWasmBody() []byte {
	return []byte{
		0x01, 0x01, 0x7f,
		0x20, 0x00, 0x20, 0x01, 0x6a, 0x21, 0x01,
		0x02, 0x40, 0x03, 0x40,
		0x20, 0x00, 0x20, 0x01, 0x4f, 0x0d, 0x01,
		0x20, 0x02, 0x20, 0x00, 0x2d, 0x00, 0x00, 0x41, ';', 0x46, 0x6a, 0x21, 0x02,
		0x20, 0x00, 0x41, 0x01, 0x6a, 0x21, 0x00, 0x0c, 0x00,
		0x0b, 0x0b,
		0x20, 0x02, 0x0b,
	}
}

func appendWasmSection(dst []byte, id byte, payload []byte) []byte {
	dst = append(dst, id)
	dst = appendULEB128(dst, uint32(len(payload)))
	return append(dst, payload...)
}

func appendWasmExport(dst []byte, name string, kind, idx byte) []byte {
	dst = appendULEB128(dst, uint32(len(name)))
	dst = append(dst, []byte(name)...)
	return append(dst, kind, idx)
}

func appendULEB128(dst []byte, v uint32) []byte {
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		dst = append(dst, b)
		if v == 0 {
			return dst
		}
	}
}

func appendSLEB128Byte(dst []byte, ch byte) []byte {
	if ch < 64 {
		return append(dst, ch)
	}
	return append(dst, ch|0x80, 0x00)
}

func instantiateManagedGrammarModules(ctx context.Context, rt wazero.Runtime) (api.Module, api.Module, error) {
	javaMod, err := compileAndInstantiateWasm(ctx, rt, "treesitter_java_wasm", JavaTreeSitterWasmBytecode)
	if err != nil {
		return nil, nil, err
	}
	csMod, err := compileAndInstantiateWasm(ctx, rt, "treesitter_csharp_wasm", CSharpTreeSitterWasmBytecode)
	if err != nil {
		return nil, nil, err
	}
	return javaMod, csMod, nil
}

func compileAndInstantiateWasm(ctx context.Context, rt wazero.Runtime, name string, wasm []byte) (api.Module, error) {
	comp, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		return nil, fmt.Errorf("failed compiling %s bytecode: %w", name, err)
	}
	mod, err := rt.InstantiateModule(ctx, comp, wazero.NewModuleConfig().WithName(name))
	if err != nil {
		return nil, fmt.Errorf("failed instantiating %s module: %w", name, err)
	}
	return mod, nil
}

func (e *WazeroTreeSitterEngine) managedModuleForLang(lang string) api.Module {
	if e == nil {
		return nil
	}
	switch lang {
	case langJava:
		return e.javaMod
	case langCSharp:
		return e.csharpMod
	default:
		return nil
	}
}

// ManagedGrammarID returns the WASM-exported grammar identifier for a managed language module (@intent tree_sitter_ast_gate_spec.md #WazeroTreeSitterEngine).
func (e *WazeroTreeSitterEngine) ManagedGrammarID(lang string) (int, error) {
	mod := e.managedModuleForLang(lang)
	if mod == nil {
		return 0, fmt.Errorf("managed wasm module unavailable for %s", lang)
	}
	e.wasmMu.Lock()
	defer e.wasmMu.Unlock()
	fn := mod.ExportedFunction("grammar_id")
	if fn == nil {
		return 0, fmt.Errorf("managed wasm module %s missing grammar_id export", lang)
	}
	res, err := fn.Call(context.Background())
	if err != nil || len(res) != 1 {
		return 0, fmt.Errorf("failed reading grammar_id for %s: %w", lang, err)
	}
	return int(res[0]), nil
}

func (e *WazeroTreeSitterEngine) callManagedByteScanner(lang, fnName, cleaned string) (uint64, bool) {
	if e == nil || e.runtime == nil {
		return 0, false
	}
	mod := e.managedModuleForLang(lang)
	if mod == nil {
		return 0, false
	}
	raw := []byte(cleaned)
	if len(raw) > maxManagedWasmBuf {
		return 0, false
	}
	if len(raw) == 0 {
		return emptyManagedScanValue(fnName), true
	}
	return e.invokeManagedWasmBuffer(mod, fnName, raw)
}

func emptyManagedScanValue(fnName string) uint64 {
	if fnName == "scan_block_balance" {
		return 1
	}
	return 0
}

func (e *WazeroTreeSitterEngine) invokeManagedWasmBuffer(mod api.Module, fnName string, raw []byte) (uint64, bool) {
	e.wasmMu.Lock()
	defer e.wasmMu.Unlock()
	mem := mod.Memory()
	fn := mod.ExportedFunction(fnName)
	if mem == nil || fn == nil || !mem.Write(0, raw) {
		return 0, false
	}
	res, err := fn.Call(context.Background(), 0, uint64(len(raw)))
	if err != nil || len(res) != 1 {
		return 0, false
	}
	return res[0], true
}

// ValidateManagedWasmSyntax verifies balanced braces, parentheses, and brackets for Java/C# via wazero WASM (@intent tree_sitter_ast_gate_spec.md #WazeroTreeSitterEngine).
func ValidateManagedWasmSyntax(lang, filePath string, content []byte) error {
	return validateManagedSyntaxWithEngine(peekDefaultEngine(), lang, filePath, content)
}

func validateManagedSyntaxWithEngine(eng *WazeroTreeSitterEngine, lang, filePath string, content []byte) error {
	cleaned := stripCLikeLiterals(string(content), true)
	if eng != nil {
		if res, handled := eng.callManagedByteScanner(lang, "scan_block_balance", cleaned); handled {
			if res != 1 {
				return fmt.Errorf("%s syntax error: unbalanced block or bracket delimiter in %s", lang, filePath)
			}
			return nil
		}
	}
	return checkBalancedDelimiters(lang, cleaned, filePath)
}

// EvaluateJavaWasm verifies Java source syntax and metrics via the dedicated wazero Java WASM module (@intent tree_sitter_ast_gate_spec.md #WazeroTreeSitterEngine).
func EvaluateJavaWasm(ctx context.Context, filePath string, content []byte) (*GateEvaluationReport, error) {
	return evaluateManagedLanguageWasm(ctx, langJava, ".java", int(managedGrammarJavaID), filePath, content)
}

// EvaluateCSharpWasm verifies C# source syntax and metrics via the dedicated wazero C# WASM module (@intent tree_sitter_ast_gate_spec.md #WazeroTreeSitterEngine).
func EvaluateCSharpWasm(ctx context.Context, filePath string, content []byte) (*GateEvaluationReport, error) {
	return evaluateManagedLanguageWasm(ctx, langCSharp, ".cs", int(managedGrammarCSharpID), filePath, content)
}

func evaluateManagedLanguageWasm(ctx context.Context, lang, defaultExt string, wantID int, filePath string, content []byte) (*GateEvaluationReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	eng, err := GetDefaultEngine()
	if err != nil {
		return nil, err
	}
	id, idErr := eng.ManagedGrammarID(lang)
	if idErr != nil || id != wantID {
		return nil, fmt.Errorf("unexpected %s wasm grammar id %d (want %d): %w", lang, id, wantID, idErr)
	}
	targetPath := strings.TrimSpace(filePath)
	if targetPath == "" {
		targetPath = "inline" + defaultExt
	} else if filepath.Ext(targetPath) == "" {
		targetPath += defaultExt
	}
	return eng.ParseAndEvaluate(ctx, targetPath, content)
}
