package goja

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"math"
	"math/rand"
	"reflect"
	"strconv"
	"time"

	"golang.org/x/text/collate"

	js_ast "github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
	"runtime"
)

const (
	sqrt1_2 float64 = math.Sqrt2 / 2
)

var (
	typeCallable = reflect.TypeOf(Callable(nil))
	typeValue    = reflect.TypeOf((*Value)(nil)).Elem()
	typeTime     = reflect.TypeOf(time.Time{})
)

type iterationKind int

const (
	iterationKindKey iterationKind = iota
	iterationKindValue
	iterationKindKeyValue
)

type global struct {
	Object   *Object
	Array    *Object
	Function *Object
	String   *Object
	Number   *Object
	Boolean  *Object
	RegExp   *Object
	Date     *Object
	Symbol   *Object
	Proxy    *Object

	ArrayBuffer *Object
	WeakSet     *Object
	WeakMap     *Object
	Map         *Object
	Set         *Object

	Error          *Object
	TypeError      *Object
	ReferenceError *Object
	SyntaxError    *Object
	RangeError     *Object
	EvalError      *Object
	URIError       *Object

	GoError *Object

	ObjectPrototype   *Object
	ArrayPrototype    *Object
	NumberPrototype   *Object
	StringPrototype   *Object
	BooleanPrototype  *Object
	FunctionPrototype *Object
	RegExpPrototype   *Object
	DatePrototype     *Object
	SymbolPrototype   *Object
	ArrayIterator     *Object

	ArrayBufferPrototype *Object
	WeakSetPrototype     *Object
	WeakMapPrototype     *Object
	MapPrototype         *Object
	SetPrototype         *Object

	IteratorPrototype      *Object
	ArrayIteratorPrototype *Object
	MapIteratorPrototype   *Object
	SetIteratorPrototype   *Object

	ErrorPrototype          *Object
	TypeErrorPrototype      *Object
	SyntaxErrorPrototype    *Object
	RangeErrorPrototype     *Object
	ReferenceErrorPrototype *Object
	EvalErrorPrototype      *Object
	URIErrorPrototype       *Object

	GoErrorPrototype *Object

	Eval *Object

	thrower         *Object
	throwerProperty Value

	regexpProtoExec Value
	weakSetAdder    *Object
	weakMapAdder    *Object
	mapAdder        *Object
	setAdder        *Object
	arrayValues     *Object
}

type Flag int

const (
	FLAG_NOT_SET Flag = iota
	FLAG_FALSE
	FLAG_TRUE
)

func (f Flag) Bool() bool {
	return f == FLAG_TRUE
}

func ToFlag(b bool) Flag {
	if b {
		return FLAG_TRUE
	}
	return FLAG_FALSE
}

type RandSource func() float64

type Now func() time.Time

type Runtime struct {
	global          global
	globalObject    *Object
	stringSingleton *stringObject
	rand            RandSource
	now             Now
	_collator       *collate.Collator

	symbolRegistry map[string]*valueSymbol

	typeInfoCache   map[reflect.Type]*reflectTypeInfo
	fieldNameMapper FieldNameMapper

	vm *vm
}

type StackFrame struct {
	prg      *Program
	funcName string
	pc       int
}

func (f *StackFrame) SrcName() string {
	if f.prg == nil {
		return "<native>"
	}
	return f.prg.src.name
}

func (f *StackFrame) FuncName() string {
	if f.funcName == "" && f.prg == nil {
		return "<native>"
	}
	if f.funcName == "" {
		return "<anonymous>"
	}
	return f.funcName
}

func (f *StackFrame) Position() Position {
	if f.prg == nil || f.prg.src == nil {
		return Position{
			0,
			0,
		}
	}
	return f.prg.src.Position(f.prg.sourceOffset(f.pc))
}

func (f *StackFrame) Write(b *bytes.Buffer) {
	if f.prg != nil {
		if n := f.prg.funcName; n != "" {
			b.WriteString(n)
			b.WriteString(" (")
		}
		if n := f.prg.src.name; n != "" {
			b.WriteString(n)
		} else {
			b.WriteString("<eval>")
		}
		b.WriteByte(':')
		b.WriteString(f.Position().String())
		b.WriteByte('(')
		b.WriteString(strconv.Itoa(f.pc))
		b.WriteByte(')')
		if f.prg.funcName != "" {
			b.WriteByte(')')
		}
	} else {
		if f.funcName != "" {
			b.WriteString(f.funcName)
			b.WriteString(" (")
		}
		b.WriteString("native")
		if f.funcName != "" {
			b.WriteByte(')')
		}
	}
}

type Exception struct {
	val   Value
	stack []StackFrame
}

type InterruptedError struct {
	Exception
	iface interface{}
}

func (e *InterruptedError) Value() interface{} {
	return e.iface
}

func (e *InterruptedError) String() string {
	if e == nil {
		return "<nil>"
	}
	var b bytes.Buffer
	if e.iface != nil {
		b.WriteString(fmt.Sprint(e.iface))
		b.WriteByte('\n')
	}
	e.writeFullStack(&b)
	return b.String()
}

func (e *InterruptedError) Error() string {
	if e == nil || e.iface == nil {
		return "<nil>"
	}
	var b bytes.Buffer
	b.WriteString(fmt.Sprint(e.iface))
	e.writeShortStack(&b)
	return b.String()
}

func (e *Exception) writeFullStack(b *bytes.Buffer) {
	for _, frame := range e.stack {
		b.WriteString("\tat ")
		frame.Write(b)
		b.WriteByte('\n')
	}
}

func (e *Exception) writeShortStack(b *bytes.Buffer) {
	if len(e.stack) > 0 && (e.stack[0].prg != nil || e.stack[0].funcName != "") {
		b.WriteString(" at ")
		e.stack[0].Write(b)
	}
}

func (e *Exception) String() string {
	if e == nil {
		return "<nil>"
	}
	var b bytes.Buffer
	if e.val != nil {
		b.WriteString(e.val.String())
		b.WriteByte('\n')
	}
	e.writeFullStack(&b)
	return b.String()
}

func (e *Exception) Error() string {
	if e == nil || e.val == nil {
		return "<nil>"
	}
	var b bytes.Buffer
	b.WriteString(e.val.String())
	e.writeShortStack(&b)
	return b.String()
}

func (e *Exception) Value() Value {
	return e.val
}

func (r *Runtime) addToGlobal(name string, value Value) {
	r.globalObject.self._putProp(name, value, true, false, true)
}

func (r *Runtime) createIterProto(val *Object) objectImpl {
	o := newBaseObjectObj(val, r.global.ObjectPrototype, classObject)

	o._putSym(symIterator, valueProp(r.newNativeFunc(r.returnThis, nil, "[Symbol.iterator]", nil, 0), true, false, true))
	return o
}

func (r *Runtime) init() {
	r.rand = rand.Float64
	r.now = time.Now
	r.global.ObjectPrototype = r.newBaseObject(nil, classObject).val
	r.globalObject = r.NewObject()

	r.vm = &vm{
		r: r,
	}
	r.vm.init()

	r.global.FunctionPrototype = r.newNativeFunc(nil, nil, "Empty", nil, 0)
	r.global.IteratorPrototype = r.newLazyObject(r.createIterProto)

	r.initObject()
	r.initFunction()
	r.initArray()
	r.initString()
	r.initNumber()
	r.initRegExp()
	r.initDate()
	r.initBoolean()
	r.initProxy()

	r.initErrors()

	r.global.Eval = r.newNativeFunc(r.builtin_eval, nil, "eval", nil, 1)
	r.addToGlobal("eval", r.global.Eval)

	r.initGlobalObject()

	r.initMath()
	r.initJSON()

	//r.initTypedArrays()
	r.initSymbol()
	r.initWeakSet()
	r.initWeakMap()
	r.initMap()
	r.initSet()

	r.global.thrower = r.newNativeFunc(r.builtin_thrower, nil, "thrower", nil, 0)
	r.global.throwerProperty = &valueProperty{
		getterFunc: r.global.thrower,
		setterFunc: r.global.thrower,
		accessor:   true,
	}
}

func (r *Runtime) typeErrorResult(throw bool, args ...interface{}) {
	if throw {
		panic(r.NewTypeError(args...))
	}
}

func (r *Runtime) newError(typ *Object, format string, args ...interface{}) Value {
	msg := fmt.Sprintf(format, args...)
	return r.builtin_new(typ, []Value{newStringValue(msg)})
}

func (r *Runtime) throwReferenceError(name string) {
	panic(r.newError(r.global.ReferenceError, "%s is not defined", name))
}

func (r *Runtime) newSyntaxError(msg string, offset int) Value {
	return r.builtin_new(r.global.SyntaxError, []Value{newStringValue(msg)})
}

func newBaseObjectObj(obj, proto *Object, class string) *baseObject {
	o := &baseObject{
		class:      class,
		val:        obj,
		extensible: true,
		prototype:  proto,
	}
	obj.self = o
	o.init()
	return o
}

func (r *Runtime) newBaseObject(proto *Object, class string) (o *baseObject) {
	v := &Object{runtime: r}
	return newBaseObjectObj(v, proto, class)
}

func (r *Runtime) NewObject() (v *Object) {
	return r.newBaseObject(r.global.ObjectPrototype, classObject).val
}

// CreateObject creates an object with given prototype. Equivalent of Object.create(proto).
func (r *Runtime) CreateObject(proto *Object) *Object {
	return r.newBaseObject(proto, classObject).val
}

func (r *Runtime) NewTypeError(args ...interface{}) *Object {
	msg := ""
	if len(args) > 0 {
		f, _ := args[0].(string)
		msg = fmt.Sprintf(f, args[1:]...)
	}
	return r.builtin_new(r.global.TypeError, []Value{newStringValue(msg)})
}

func (r *Runtime) NewGoError(err error) *Object {
	e := r.newError(r.global.GoError, err.Error()).(*Object)
	e.Set("value", err)
	return e
}

func (r *Runtime) newFunc(name string, len int, strict bool) (f *funcObject) {
	v := &Object{runtime: r}

	f = &funcObject{}
	f.class = classFunction
	f.val = v
	f.extensible = true
	v.self = f
	f.prototype = r.global.FunctionPrototype
	f.init(name, len)
	if strict {
		f._put("caller", r.global.throwerProperty)
		f._put("arguments", r.global.throwerProperty)
	}
	return
}

func (r *Runtime) newNativeFuncObj(v *Object, call func(FunctionCall) Value, construct func(args []Value) *Object, name string, proto *Object, length int) *nativeFuncObject {
	f := &nativeFuncObject{
		baseFuncObject: baseFuncObject{
			baseObject: baseObject{
				class:      classFunction,
				val:        v,
				extensible: true,
				prototype:  r.global.FunctionPrototype,
			},
		},
		f:         call,
		construct: construct,
	}
	v.self = f
	f.init(name, length)
	if proto != nil {
		f._putProp("prototype", proto, false, false, false)
	}
	return f
}

func (r *Runtime) newNativeConstructor(call func(ConstructorCall) *Object, name string, length int) *Object {
	v := &Object{runtime: r}

	f := &nativeFuncObject{
		baseFuncObject: baseFuncObject{
			baseObject: baseObject{
				class:      classFunction,
				val:        v,
				extensible: true,
				prototype:  r.global.FunctionPrototype,
			},
		},
	}

	f.f = func(c FunctionCall) Value {
		return f.defaultConstruct(call, c.Arguments)
	}

	f.construct = func(args []Value) *Object {
		return f.defaultConstruct(call, args)
	}

	v.self = f
	f.init(name, length)

	proto := r.NewObject()
	proto.self._putProp("constructor", v, true, false, true)
	f._putProp("prototype", proto, true, false, false)

	return v
}

func (r *Runtime) newNativeFunc(call func(FunctionCall) Value, construct func(args []Value) *Object, name string, proto *Object, length int) *Object {
	v := &Object{runtime: r}

	f := &nativeFuncObject{
		baseFuncObject: baseFuncObject{
			baseObject: baseObject{
				class:      classFunction,
				val:        v,
				extensible: true,
				prototype:  r.global.FunctionPrototype,
			},
		},
		f:         call,
		construct: construct,
	}
	v.self = f
	f.init(name, length)
	if proto != nil {
		f._putProp("prototype", proto, false, false, false)
		proto.self._putProp("constructor", v, true, false, true)
	}
	return v
}

func (r *Runtime) newNativeFuncConstructObj(v *Object, construct func(args []Value, proto *Object) *Object, name string, proto *Object, length int) *nativeFuncObject {
	f := &nativeFuncObject{
		baseFuncObject: baseFuncObject{
			baseObject: baseObject{
				class:      classFunction,
				val:        v,
				extensible: true,
				prototype:  r.global.FunctionPrototype,
			},
		},
		f: r.constructWrap(construct, proto),
		construct: func(args []Value) *Object {
			return construct(args, proto)
		},
	}

	f.init(name, length)
	if proto != nil {
		f._putProp("prototype", proto, false, false, false)
	}
	return f
}

func (r *Runtime) newNativeFuncConstruct(construct func(args []Value, proto *Object) *Object, name string, prototype *Object, length int) *Object {
	return r.newNativeFuncConstructProto(construct, name, prototype, r.global.FunctionPrototype, length)
}

func (r *Runtime) newNativeFuncConstructProto(construct func(args []Value, proto *Object) *Object, name string, prototype, proto *Object, length int) *Object {
	v := &Object{runtime: r}

	f := &nativeFuncObject{}
	f.class = classFunction
	f.val = v
	f.extensible = true
	v.self = f
	f.prototype = proto
	f.f = r.constructWrap(construct, prototype)
	f.construct = func(args []Value) *Object {
		return construct(args, prototype)
	}
	f.init(name, length)
	if prototype != nil {
		f._putProp("prototype", prototype, false, false, false)
		prototype.self._putProp("constructor", v, true, false, true)
	}
	return v
}

func (r *Runtime) newPrimitiveObject(value Value, proto *Object, class string) *Object {
	v := &Object{runtime: r}

	o := &primitiveValueObject{}
	o.class = class
	o.val = v
	o.extensible = true
	v.self = o
	o.prototype = proto
	o.pValue = value
	o.init()
	return v
}

func (r *Runtime) builtin_Number(call FunctionCall) Value {
	if len(call.Arguments) > 0 {
		return call.Arguments[0].ToNumber()
	} else {
		return intToValue(0)
	}
}

func (r *Runtime) builtin_newNumber(args []Value) *Object {
	var v Value
	if len(args) > 0 {
		v = args[0].ToNumber()
	} else {
		v = intToValue(0)
	}
	return r.newPrimitiveObject(v, r.global.NumberPrototype, classNumber)
}

func (r *Runtime) builtin_Boolean(call FunctionCall) Value {
	if len(call.Arguments) > 0 {
		if call.Arguments[0].ToBoolean() {
			return valueTrue
		} else {
			return valueFalse
		}
	} else {
		return valueFalse
	}
}

func (r *Runtime) builtin_newBoolean(args []Value) *Object {
	var v Value
	if len(args) > 0 {
		if args[0].ToBoolean() {
			v = valueTrue
		} else {
			v = valueFalse
		}
	} else {
		v = valueFalse
	}
	return r.newPrimitiveObject(v, r.global.BooleanPrototype, classBoolean)
}

func (r *Runtime) error_toString(call FunctionCall) Value {
	obj := call.This.ToObject(r).self
	msg := obj.getStr("message", nil)
	name := obj.getStr("name", nil)
	var nameStr, msgStr string
	if name != nil && name != _undefined {
		nameStr = name.String()
	}
	if msg != nil && msg != _undefined {
		msgStr = msg.String()
	}
	if nameStr != "" && msgStr != "" {
		return newStringValue(fmt.Sprintf("%s: %s", name.String(), msgStr))
	} else {
		if nameStr != "" {
			return name.toString()
		} else {
			return msg.toString()
		}
	}
}

func (r *Runtime) builtin_Error(args []Value, proto *Object) *Object {
	obj := r.newBaseObject(proto, classError)
	if len(args) > 0 && args[0] != _undefined {
		obj._putProp("message", args[0], true, false, true)
	}
	return obj.val
}

func getConstructor(construct *Object) func(args []Value) *Object {
repeat:
	switch f := construct.self.(type) {
	case *nativeFuncObject:
		if f.construct != nil {
			return f.construct
		}
	case *boundFuncObject:
		if f.construct != nil {
			return f.construct
		}
	case *funcObject:
		return f.construct
	case *lazyObject:
		construct.self = f.create(construct)
		goto repeat
	case *proxyObject:
		if f.ctor != nil {
			return f.construct
		}
		return nil
	}

	return nil
}

func (r *Runtime) builtin_new(construct *Object, args []Value) *Object {
	f := getConstructor(construct)
	if f == nil {
		panic("Not a constructor")
	}
	return f(args)
}

func (r *Runtime) throw(e Value) {
	panic(e)
}

func (r *Runtime) builtin_thrower(FunctionCall) Value {
	r.typeErrorResult(true, "'caller', 'callee', and 'arguments' properties may not be accessed on strict mode functions or the arguments objects for calls to them")
	return nil
}

func (r *Runtime) eval(src string, direct, strict bool, this Value) Value {

	p, err := r.compile("<eval>", src, strict, true)
	if err != nil {
		panic(err)
	}

	vm := r.vm

	vm.pushCtx()
	vm.prg = p
	vm.pc = 0
	if !direct {
		vm.stash = nil
	}
	vm.sb = vm.sp
	vm.push(this)
	if strict {
		vm.push(valueTrue)
	} else {
		vm.push(valueFalse)
	}
	vm.run()
	vm.popCtx()
	vm.halt = false
	retval := vm.stack[vm.sp-1]
	vm.sp -= 2
	return retval
}

func (r *Runtime) builtin_eval(call FunctionCall) Value {
	if len(call.Arguments) == 0 {
		return _undefined
	}
	if str, ok := call.Arguments[0].assertString(); ok {
		return r.eval(str.String(), false, false, r.globalObject)
	}
	return call.Arguments[0]
}

func (r *Runtime) constructWrap(construct func(args []Value, proto *Object) *Object, proto *Object) func(call FunctionCall) Value {
	return func(call FunctionCall) Value {
		return construct(call.Arguments, proto)
	}
}

func (r *Runtime) toCallable(v Value) func(FunctionCall) Value {
	if call, ok := r.toObject(v).self.assertCallable(); ok {
		return call
	}
	r.typeErrorResult(true, "Value is not callable: %s", v.toString())
	return nil
}

func (r *Runtime) checkObjectCoercible(v Value) {
	switch v.(type) {
	case valueUndefined, valueNull:
		r.typeErrorResult(true, "Value is not object coercible")
	}
}

func toUInt32(v Value) uint32 {
	v = v.ToNumber()
	if i, ok := v.assertInt(); ok {
		return uint32(i)
	}

	if f, ok := v.assertFloat(); ok {
		if !math.IsNaN(f) && !math.IsInf(f, 0) {
			return uint32(int64(f))
		}
	}
	return 0
}

func toUInt16(v Value) uint16 {
	v = v.ToNumber()
	if i, ok := v.assertInt(); ok {
		return uint16(i)
	}

	if f, ok := v.assertFloat(); ok {
		if !math.IsNaN(f) && !math.IsInf(f, 0) {
			return uint16(int64(f))
		}
	}
	return 0
}

func toLength(v Value) int64 {
	if v == nil {
		return 0
	}
	i := v.ToInteger()
	if i < 0 {
		return 0
	}
	if i >= maxInt {
		return maxInt - 1
	}
	return i
}

func toInt32(v Value) int32 {
	v = v.ToNumber()
	if i, ok := v.assertInt(); ok {
		return int32(i)
	}

	if f, ok := v.assertFloat(); ok {
		if !math.IsNaN(f) && !math.IsInf(f, 0) {
			return int32(int64(f))
		}
	}
	return 0
}

func (r *Runtime) toBoolean(b bool) Value {
	if b {
		return valueTrue
	} else {
		return valueFalse
	}
}

// New creates an instance of a Javascript runtime that can be used to run code. Multiple instances may be created and
// used simultaneously, however it is not possible to pass JS values across runtimes.
func New() *Runtime {
	r := &Runtime{}
	r.init()
	return r
}

// Compile creates an internal representation of the JavaScript code that can be later run using the Runtime.RunProgram()
// method. This representation is not linked to a runtime in any way and can be run in multiple runtimes (possibly
// at the same time).
func Compile(name, src string, strict bool) (*Program, error) {
	return compile(name, src, strict, false)
}

// CompileAST creates an internal representation of the JavaScript code that can be later run using the Runtime.RunProgram()
// method. This representation is not linked to a runtime in any way and can be run in multiple runtimes (possibly
// at the same time).
func CompileAST(prg *js_ast.Program, strict bool) (*Program, error) {
	return compileAST(prg, strict, false)
}

// MustCompile is like Compile but panics if the code cannot be compiled.
// It simplifies safe initialization of global variables holding compiled JavaScript code.
func MustCompile(name, src string, strict bool) *Program {
	prg, err := Compile(name, src, strict)
	if err != nil {
		panic(err)
	}

	return prg
}

func compile(name, src string, strict, eval bool) (p *Program, err error) {
	prg, err1 := parser.ParseFile(nil, name, src, 0)
	if err1 != nil {
		switch err1 := err1.(type) {
		case parser.ErrorList:
			if len(err1) > 0 && err1[0].Message == "Invalid left-hand side in assignment" {
				err = &CompilerReferenceError{
					CompilerError: CompilerError{
						Message: err1.Error(),
					},
				}
				return
			}
		}
		// FIXME offset
		err = &CompilerSyntaxError{
			CompilerError: CompilerError{
				Message: err1.Error(),
			},
		}
		return
	}

	p, err = compileAST(prg, strict, eval)

	return
}

func compileAST(prg *js_ast.Program, strict, eval bool) (p *Program, err error) {
	c := newCompiler()
	c.scope.strict = strict
	c.scope.eval = eval

	defer func() {
		if x := recover(); x != nil {
			p = nil
			switch x1 := x.(type) {
			case *CompilerSyntaxError:
				err = x1
			default:
				panic(x)
			}
		}
	}()

	c.compile(prg)
	p = c.p
	return
}

func (r *Runtime) compile(name, src string, strict, eval bool) (p *Program, err error) {
	p, err = compile(name, src, strict, eval)
	if err != nil {
		switch x1 := err.(type) {
		case *CompilerSyntaxError:
			err = &Exception{
				val: r.builtin_new(r.global.SyntaxError, []Value{newStringValue(x1.Error())}),
			}
		case *CompilerReferenceError:
			err = &Exception{
				val: r.newError(r.global.ReferenceError, x1.Message),
			} // TODO proper message
		}
	}
	return
}

// RunString executes the given string in the global context.
func (r *Runtime) RunString(str string) (Value, error) {
	return r.RunScript("", str)
}

// RunScript executes the given string in the global context.
func (r *Runtime) RunScript(name, src string) (Value, error) {
	p, err := Compile(name, src, false)

	if err != nil {
		return nil, err
	}

	return r.RunProgram(p)
}

// RunProgram executes a pre-compiled (see Compile()) code in the global context.
func (r *Runtime) RunProgram(p *Program) (result Value, err error) {
	defer func() {
		if x := recover(); x != nil {
			if intr, ok := x.(*InterruptedError); ok {
				err = intr
			} else {
				panic(x)
			}
		}
	}()
	recursive := false
	if len(r.vm.callStack) > 0 {
		recursive = true
		r.vm.pushCtx()
	}
	r.vm.prg = p
	r.vm.pc = 0
	ex := r.vm.runTry()
	if ex == nil {
		result = r.vm.pop()
	} else {
		err = ex
	}
	if recursive {
		r.vm.popCtx()
		r.vm.halt = false
		r.vm.clearStack()
	} else {
		r.vm.stack = nil
	}
	return
}

func (r *Runtime) CaptureCallStack(n int) []StackFrame {
	m := len(r.vm.callStack)
	if n > 0 {
		m -= m - n
	} else {
		m = 0
	}
	stackFrames := make([]StackFrame, 0)
	stackFrames = r.vm.captureStack(stackFrames, m)
	return stackFrames
}

// Interrupt a running JavaScript. The corresponding Go call will return an *InterruptedError containing v.
// Note, it only works while in JavaScript code, it does not interrupt native Go functions (which includes all built-ins).
// If the runtime is currently not running, it will be immediately interrupted on the next Run*() call.
// To avoid that use ClearInterrupt()
func (r *Runtime) Interrupt(v interface{}) {
	r.vm.Interrupt(v)
}

// ClearInterrupt resets the interrupt flag. Typically this needs to be called before the runtime
// is made available for re-use if there is a chance it could have been interrupted with Interrupt().
// Otherwise if Interrupt() was called when runtime was not running (e.g. if it had already finished)
// so that Interrupt() didn't actually trigger, an attempt to use the runtime will immediately cause
// an interruption. It is up to the user to ensure proper synchronisation so that ClearInterrupt() is
// only called when the runtime has finished and there is no chance of a concurrent Interrupt() call.
func (r *Runtime) ClearInterrupt() {
	r.vm.ClearInterrupt()
}

/*
ToValue converts a Go value into JavaScript value.

Primitive types (ints and uints, floats, string, bool) are converted to the corresponding JavaScript primitives.

func(FunctionCall) Value is treated as a native JavaScript function.

map[string]interface{} is converted into a host object that largely behaves like a JavaScript Object.

[]interface{} is converted into a host object that behaves largely like a JavaScript Array, however it's not extensible
because extending it can change the pointer so it becomes detached from the original.

*[]interface{} same as above, but the array becomes extensible.

A function is wrapped within a native JavaScript function. When called the arguments are automatically converted to
the appropriate Go types. If conversion is not possible, a TypeError is thrown.

A slice type is converted into a generic reflect based host object that behaves similar to an unexpandable Array.

Any other type is converted to a generic reflect based host object. Depending on the underlying type it behaves similar
to a Number, String, Boolean or Object.

Note that the underlying type is not lost, calling Export() returns the original Go value. This applies to all
reflect based types.
*/
func (r *Runtime) ToValue(i interface{}) Value {
	switch i := i.(type) {
	case nil:
		return _null
	case Value:
		// TODO: prevent importing Objects from a different runtime
		return i
	case string:
		return newStringValue(i)
	case bool:
		if i {
			return valueTrue
		} else {
			return valueFalse
		}
	case func(FunctionCall) Value:
		name := runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Name()
		return r.newNativeFunc(i, nil, name, nil, 0)
	case func(ConstructorCall) *Object:
		name := runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Name()
		return r.newNativeConstructor(i, name, 0)
	case *Proxy:
		proxy := i.proxy.val
		if proxy.runtime != r {
			r.typeErrorResult(true, "Illegal runtime transition for proxy")
		}
		return proxy
	case int:
		return intToValue(int64(i))
	case int8:
		return intToValue(int64(i))
	case int16:
		return intToValue(int64(i))
	case int32:
		return intToValue(int64(i))
	case int64:
		return intToValue(i)
	case uint:
		if uint64(i) <= math.MaxInt64 {
			return intToValue(int64(i))
		} else {
			return floatToValue(float64(i))
		}
	case uint8:
		return intToValue(int64(i))
	case uint16:
		return intToValue(int64(i))
	case uint32:
		return intToValue(int64(i))
	case uint64:
		if i <= math.MaxInt64 {
			return intToValue(int64(i))
		}
		return floatToValue(float64(i))
	case float32:
		return floatToValue(float64(i))
	case float64:
		return floatToValue(i)
	case map[string]interface{}:
		if i == nil {
			return _null
		}
		obj := &Object{runtime: r}
		m := &objectGoMapSimple{
			baseObject: baseObject{
				val:        obj,
				extensible: true,
			},
			data: i,
		}
		obj.self = m
		m.init()
		return obj
	case []interface{}:
		if i == nil {
			return _null
		}
		obj := &Object{runtime: r}
		a := &objectGoSlice{
			baseObject: baseObject{
				val: obj,
			},
			data: &i,
		}
		obj.self = a
		a.init()
		return obj
	case *[]interface{}:
		if i == nil {
			return _null
		}
		obj := &Object{runtime: r}
		a := &objectGoSlice{
			baseObject: baseObject{
				val: obj,
			},
			data:            i,
			sliceExtensible: true,
		}
		obj.self = a
		a.init()
		return obj
	}

	origValue := reflect.ValueOf(i)
	value := origValue
	for value.Kind() == reflect.Ptr {
		value = reflect.Indirect(value)
	}

	if !value.IsValid() {
		return _null
	}

	switch value.Kind() {
	case reflect.Map:
		if value.Type().NumMethod() == 0 {
			switch value.Type().Key().Kind() {
			case reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
				reflect.Float64, reflect.Float32:

				obj := &Object{runtime: r}
				m := &objectGoMapReflect{
					objectGoReflect: objectGoReflect{
						baseObject: baseObject{
							val:        obj,
							extensible: true,
						},
						origValue: origValue,
						value:     value,
					},
				}
				m.init()
				obj.self = m
				return obj
			}
		}
	case reflect.Slice:
		obj := &Object{runtime: r}
		a := &objectGoSliceReflect{
			objectGoReflect: objectGoReflect{
				baseObject: baseObject{
					val: obj,
				},
				origValue: origValue,
				value:     value,
			},
		}
		a.init()
		obj.self = a
		return obj
	case reflect.Func:
		name := runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Name()
		return r.newNativeFunc(r.wrapReflectFunc(value), nil, name, nil, value.Type().NumIn())
	}

	obj := &Object{runtime: r}
	o := &objectGoReflect{
		baseObject: baseObject{
			val: obj,
		},
		origValue: origValue,
		value:     value,
	}
	obj.self = o
	o.init()
	return obj
}

func (r *Runtime) wrapReflectFunc(value reflect.Value) func(FunctionCall) Value {
	return func(call FunctionCall) Value {
		typ := value.Type()
		nargs := typ.NumIn()
		var in []reflect.Value

		if l := len(call.Arguments); l < nargs {
			// fill missing arguments with zero values
			n := nargs
			if typ.IsVariadic() {
				n--
			}
			in = make([]reflect.Value, n)
			for i := l; i < n; i++ {
				in[i] = reflect.Zero(typ.In(i))
			}
		} else {
			if l > nargs && !typ.IsVariadic() {
				l = nargs
			}
			in = make([]reflect.Value, l)
		}

		callSlice := false
		for i, a := range call.Arguments {
			var t reflect.Type

			n := i
			if n >= nargs-1 && typ.IsVariadic() {
				if n > nargs-1 {
					n = nargs - 1
				}

				t = typ.In(n).Elem()
			} else if n > nargs-1 { // ignore extra arguments
				break
			} else {
				t = typ.In(n)
			}

			// if this is a variadic Go function, and the caller has supplied
			// exactly the number of JavaScript arguments required, and this
			// is the last JavaScript argument, try treating the it as the
			// actual set of variadic Go arguments. if that succeeds, break
			// out of the loop.
			if typ.IsVariadic() && len(call.Arguments) == nargs && i == nargs-1 {
				if v, err := r.toReflectValue(a, typ.In(n)); err == nil {
					in[i] = v
					callSlice = true
					break
				}
			}
			var err error
			in[i], err = r.toReflectValue(a, t)
			if err != nil {
				panic(r.newError(r.global.TypeError, "Could not convert function call parameter %v to %v", a, t))
			}
		}

		var out []reflect.Value
		if callSlice {
			out = value.CallSlice(in)
		} else {
			out = value.Call(in)
		}

		if len(out) == 0 {
			return _undefined
		}

		if last := out[len(out)-1]; last.Type().Name() == "error" {
			if !last.IsNil() {
				err := last.Interface()
				if _, ok := err.(*Exception); ok {
					panic(err)
				}
				panic(r.NewGoError(last.Interface().(error)))
			}
			out = out[:len(out)-1]
		}

		switch len(out) {
		case 0:
			return _undefined
		case 1:
			return r.ToValue(out[0].Interface())
		default:
			s := make([]interface{}, len(out))
			for i, v := range out {
				s[i] = v.Interface()
			}

			return r.ToValue(s)
		}
	}
}

func (r *Runtime) toReflectValue(v Value, typ reflect.Type) (reflect.Value, error) {
	switch typ.Kind() {
	case reflect.String:
		return reflect.ValueOf(v.String()).Convert(typ), nil
	case reflect.Bool:
		return reflect.ValueOf(v.ToBoolean()).Convert(typ), nil
	case reflect.Int:
		i, _ := toInt(v)
		return reflect.ValueOf(int(i)).Convert(typ), nil
	case reflect.Int64:
		i, _ := toInt(v)
		return reflect.ValueOf(i).Convert(typ), nil
	case reflect.Int32:
		i, _ := toInt(v)
		return reflect.ValueOf(int32(i)).Convert(typ), nil
	case reflect.Int16:
		i, _ := toInt(v)
		return reflect.ValueOf(int16(i)).Convert(typ), nil
	case reflect.Int8:
		i, _ := toInt(v)
		return reflect.ValueOf(int8(i)).Convert(typ), nil
	case reflect.Uint:
		i, _ := toInt(v)
		return reflect.ValueOf(uint(i)).Convert(typ), nil
	case reflect.Uint64:
		i, _ := toInt(v)
		return reflect.ValueOf(uint64(i)).Convert(typ), nil
	case reflect.Uint32:
		i, _ := toInt(v)
		return reflect.ValueOf(uint32(i)).Convert(typ), nil
	case reflect.Uint16:
		i, _ := toInt(v)
		return reflect.ValueOf(uint16(i)).Convert(typ), nil
	case reflect.Uint8:
		i, _ := toInt(v)
		return reflect.ValueOf(uint8(i)).Convert(typ), nil
	}

	if typ == typeCallable {
		if fn, ok := AssertFunction(v); ok {
			return reflect.ValueOf(fn), nil
		}
	}

	if typ.Implements(typeValue) {
		return reflect.ValueOf(v), nil
	}

	et := v.ExportType()
	if et == nil {
		return reflect.Zero(typ), nil
	}
	if et.AssignableTo(typ) {
		return reflect.ValueOf(v.Export()), nil
	} else if et.ConvertibleTo(typ) {
		return reflect.ValueOf(v.Export()).Convert(typ), nil
	}

	if typ == typeTime && et.Kind() == reflect.String {
		tme, ok := dateParse(v.String())
		if !ok {
			return reflect.Value{}, fmt.Errorf("Could not convert string %v to %v", v, typ)
		}
		return reflect.ValueOf(tme), nil
	}

	switch typ.Kind() {
	case reflect.Slice:
		if o, ok := v.(*Object); ok {
			if o.self.className() == classArray {
				l := int(toLength(o.self.getStr("length", nil)))
				s := reflect.MakeSlice(typ, l, l)
				elemTyp := typ.Elem()
				for i := 0; i < l; i++ {
					item := o.self.get(intToValue(int64(i)), nil)
					itemval, err := r.toReflectValue(item, elemTyp)
					if err != nil {
						return reflect.Value{}, fmt.Errorf("Could not convert array element %v to %v at %d: %s", v, typ, i, err)
					}
					s.Index(i).Set(itemval)
				}
				return s, nil
			}
		}
	case reflect.Map:
		if o, ok := v.(*Object); ok {
			m := reflect.MakeMap(typ)
			keyTyp := typ.Key()
			elemTyp := typ.Elem()
			needConvertKeys := !reflect.ValueOf("").Type().AssignableTo(keyTyp)
			for _, itemName := range o.self.ownKeys(false, nil) {
				var kv reflect.Value
				var err error
				if needConvertKeys {
					kv, err = r.toReflectValue(itemName, keyTyp)
					if err != nil {
						return reflect.Value{}, fmt.Errorf("Could not convert map key %s to %v", itemName.String(), typ)
					}
				} else {
					kv = reflect.ValueOf(itemName.String())
				}

				ival := o.self.get(itemName, nil)
				if ival != nil {
					vv, err := r.toReflectValue(ival, elemTyp)
					if err != nil {
						return reflect.Value{}, fmt.Errorf("Could not convert map value %v to %v at key %s", ival, typ, itemName.String())
					}
					m.SetMapIndex(kv, vv)
				} else {
					m.SetMapIndex(kv, reflect.Zero(elemTyp))
				}

			}
			return m, nil
		}
	case reflect.Struct:
		if o, ok := v.(*Object); ok {
			s := reflect.New(typ).Elem()
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				if ast.IsExported(field.Name) {
					name := field.Name
					if r.fieldNameMapper != nil {
						name = r.fieldNameMapper.FieldName(typ, field)
					}
					var v Value
					if field.Anonymous {
						v = o
					} else {
						v = o.self.getStr(name, nil)
					}

					if v != nil {
						vv, err := r.toReflectValue(v, field.Type)
						if err != nil {
							return reflect.Value{}, fmt.Errorf("Could not convert struct value %v to %v for field %s: %s", v, field.Type, field.Name, err)

						}
						s.Field(i).Set(vv)
					}
				}
			}
			return s, nil
		}
	case reflect.Func:
		if fn, ok := AssertFunction(v); ok {
			return reflect.MakeFunc(typ, r.wrapJSFunc(fn, typ)), nil
		}
	case reflect.Ptr:
		elemTyp := typ.Elem()
		v, err := r.toReflectValue(v, elemTyp)
		if err != nil {
			return reflect.Value{}, err
		}

		ptrVal := reflect.New(v.Type())
		ptrVal.Elem().Set(v)

		return ptrVal, nil
	}

	return reflect.Value{}, fmt.Errorf("Could not convert %v to %v", v, typ)
}

func (r *Runtime) wrapJSFunc(fn Callable, typ reflect.Type) func(args []reflect.Value) (results []reflect.Value) {
	return func(args []reflect.Value) (results []reflect.Value) {
		jsArgs := make([]Value, len(args))
		for i, arg := range args {
			jsArgs[i] = r.ToValue(arg.Interface())
		}

		results = make([]reflect.Value, typ.NumOut())
		res, err := fn(_undefined, jsArgs...)
		if err == nil {
			if typ.NumOut() > 0 {
				results[0], err = r.toReflectValue(res, typ.Out(0))
			}
		}

		if err != nil {
			if typ.NumOut() == 2 && typ.Out(1).Name() == "error" {
				results[1] = reflect.ValueOf(err).Convert(typ.Out(1))
			} else {
				panic(err)
			}
		}

		for i, v := range results {
			if !v.IsValid() {
				results[i] = reflect.Zero(typ.Out(i))
			}
		}

		return
	}
}

// ExportTo converts a JavaScript value into the specified Go value. The second parameter must be a non-nil pointer.
// Returns error if conversion is not possible.
func (r *Runtime) ExportTo(v Value, target interface{}) error {
	tval := reflect.ValueOf(target)
	if tval.Kind() != reflect.Ptr || tval.IsNil() {
		return errors.New("target must be a non-nil pointer")
	}
	vv, err := r.toReflectValue(v, tval.Elem().Type())
	if err != nil {
		return err
	}
	tval.Elem().Set(vv)
	return nil
}

// GlobalObject returns the global object.
func (r *Runtime) GlobalObject() *Object {
	return r.globalObject
}

// Set the specified value as a property of the global object.
// The value is first converted using ToValue()
func (r *Runtime) Set(name string, value interface{}) {
	r.globalObject.self.setOwnStr(name, r.ToValue(value), false)
}

// Get the specified property of the global object.
func (r *Runtime) Get(name string) Value {
	return r.globalObject.self.getStr(name, nil)
}

// SetRandSource sets random source for this Runtime. If not called, the default math/rand is used.
func (r *Runtime) SetRandSource(source RandSource) {
	r.rand = source
}

// SetTimeSource sets the current time source for this Runtime.
// If not called, the default time.Now() is used.
func (r *Runtime) SetTimeSource(now Now) {
	r.now = now
}

// New is an equivalent of the 'new' operator allowing to call it directly from Go.
func (r *Runtime) New(construct Value, args ...Value) (o *Object, err error) {
	defer func() {
		if x := recover(); x != nil {
			switch x := x.(type) {
			case *Exception:
				err = x
			case *InterruptedError:
				err = x
			default:
				panic(x)
			}
		}
	}()
	return r.builtin_new(r.toObject(construct), args), nil
}

// Callable represents a JavaScript function that can be called from Go.
type Callable func(this Value, args ...Value) (Value, error)

// AssertFunction checks if the Value is a function and returns a Callable.
func AssertFunction(v Value) (Callable, bool) {
	if obj, ok := v.(*Object); ok {
		if f, ok := obj.self.assertCallable(); ok {
			return func(this Value, args ...Value) (ret Value, err error) {
				defer func() {
					if x := recover(); x != nil {
						if ex, ok := x.(*InterruptedError); ok {
							err = ex
						} else {
							panic(x)
						}
					}
				}()
				ex := obj.runtime.vm.try(func() {
					ret = f(FunctionCall{
						This:      this,
						Arguments: args,
					})
				})
				if ex != nil {
					err = ex
				}
				obj.runtime.vm.clearStack()
				return
			}, true
		}
	}
	return nil, false
}

// IsUndefined returns true if the supplied Value is undefined. Note, it checks against the real undefined, not
// against the global object's 'undefined' property.
func IsUndefined(v Value) bool {
	return v == _undefined
}

// IsNull returns true if the supplied Value is null.
func IsNull(v Value) bool {
	return v == _null
}

// IsNaN returns true if the supplied value is NaN.
func IsNaN(v Value) bool {
	f, ok := v.assertFloat()
	return ok && math.IsNaN(f)
}

// IsInfinity returns true if the supplied is (+/-)Infinity
func IsInfinity(v Value) bool {
	return v == _positiveInf || v == _negativeInf
}

// Undefined returns JS undefined value. Note if global 'undefined' property is changed this still returns the original value.
func Undefined() Value {
	return _undefined
}

// Null returns JS null value.
func Null() Value {
	return _null
}

// NaN returns a JS NaN value.
func NaN() Value {
	return _NaN
}

// PositiveInf returns a JS +Inf value.
func PositiveInf() Value {
	return _positiveInf
}

// NegativeInf returns a JS -Inf value.
func NegativeInf() Value {
	return _negativeInf
}

func tryFunc(f func()) (err error) {
	defer func() {
		if x := recover(); x != nil {
			switch x := x.(type) {
			case *Exception:
				err = x
			case *InterruptedError:
				err = x
			case Value:
				err = &Exception{
					val: x,
				}
			default:
				panic(x)
			}
		}
	}()

	f()

	return nil
}

func (r *Runtime) toObject(v Value, args ...interface{}) *Object {
	if obj, ok := v.(*Object); ok {
		return obj
	}
	if len(args) > 0 {
		panic(r.NewTypeError(args...))
	} else {
		var s string
		if v == nil {
			s = "undefined"
		} else {
			s = v.String()
		}
		panic(r.NewTypeError("Value is not an object: %s", s))
	}
}

func (r *Runtime) speciesConstructor(o, defaultConstructor *Object) func(args []Value) *Object {
	c := o.self.getStr("constructor", nil)
	if c != nil && c != _undefined {
		c = r.toObject(c).self.get(symSpecies, nil)
	}
	if c == nil || c == _undefined {
		c = defaultConstructor
	}
	return getConstructor(r.toObject(c))
}

func (r *Runtime) returnThis(call FunctionCall) Value {
	return call.This
}

func createDataPropertyOrThrow(o *Object, p Value, v Value) {
	o.self.defineOwnProperty(p, PropertyDescriptor{
		Writable:     FLAG_TRUE,
		Enumerable:   FLAG_TRUE,
		Configurable: FLAG_TRUE,
		Value:        v,
	}, true)
}

func toPropertyKey(key Value) Value {
	return key.ToPrimitiveString()
}

func (r *Runtime) getVStr(v Value, p string) Value {
	o := v.ToObject(r)
	return o.self.getStr(p, v)
}

func (r *Runtime) getV(v Value, p Value) Value {
	o := v.ToObject(r)
	return o.self.get(p, v)
}

func (r *Runtime) getIterator(obj Value, method func(FunctionCall) Value) *Object {
	if method == nil {
		method = toMethod(r.getV(obj, symIterator))
		if method == nil {
			panic(r.NewTypeError("object is not iterable"))
		}
	}

	return r.toObject(method(FunctionCall{
		This: obj,
	}))
}

func (r *Runtime) iterate(iter *Object, step func(Value)) {
	for {
		res := r.toObject(toMethod(iter.self.getStr("next", nil))(FunctionCall{This: iter}))
		if nilSafe(res.self.getStr("done", nil)).ToBoolean() {
			break
		}
		err := tryFunc(func() {
			step(nilSafe(res.self.getStr("value", nil)))
		})
		if err != nil {
			retMethod := toMethod(iter.self.getStr("return", nil))
			if retMethod != nil {
				_ = tryFunc(func() {
					retMethod(FunctionCall{This: iter})
				})
			}
			panic(err)
		}
	}
}

func (r *Runtime) createIterResultObject(value Value, done bool) Value {
	o := r.NewObject()
	o.self.setOwnStr("value", value, false)
	o.self.setOwnStr("done", r.toBoolean(done), false)
	return o
}

func (r *Runtime) newLazyObject(create func(*Object) objectImpl) *Object {
	val := &Object{runtime: r}
	o := &lazyObject{
		val:    val,
		create: create,
	}
	val.self = o
	return val
}

func (r *Runtime) constructorThrower(name string) func(call FunctionCall) Value {
	return func(FunctionCall) Value {
		panic(r.NewTypeError("Constructor %s requires 'new'", name))
	}
}

func nilSafe(v Value) Value {
	if v != nil {
		return v
	}
	return _undefined
}

func isArray(object *Object) bool {
	self := object.self
	if proxy, ok := self.(*proxyObject); ok {
		if proxy.target == nil {
			panic(typeError("Cannot perform 'IsArray' on a proxy that has been revoked"))
		}
		return isArray(proxy.target)
	}
	switch self.className() {
	case classArray:
		return true
	default:
		return false
	}
}
