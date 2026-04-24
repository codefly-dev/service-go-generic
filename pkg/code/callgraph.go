package code

import (
	"fmt"
	"go/types"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/vta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// CallEdge is a call relationship between two functions.
type CallEdge struct {
	CallerID   string `json:"caller_id"`   // "pkg.Func" or "(*pkg.Type).Method"
	CalleeID   string `json:"callee_id"`
	CallerName string `json:"caller_name"`
	CalleeName string `json:"callee_name"`
	CallType   string `json:"call_type"`   // "static", "interface", "closure"
	File       string `json:"file"`        // call site file (relative)
	Line       int    `json:"line"`        // call site line
}

// ImplementsEdge is a type-implements-interface relationship.
type ImplementsEdge struct {
	TypeID      string `json:"type_id"`
	TypeName    string `json:"type_name"`
	InterfaceID string `json:"interface_id"`
	InterfaceName string `json:"interface_name"`
}

// CallGraphResult is the full result of VTA analysis.
type CallGraphResult struct {
	Calls      []CallEdge      `json:"calls"`
	Implements []ImplementsEdge `json:"implements"`
	Module     string           `json:"module"`
	Error      string           `json:"error,omitempty"`
}

// ComputeCallGraph runs VTA call graph analysis on the Go module.
// Exported so the Tooling layer can invoke it without going through the
// Code.Execute bus.
func (c *Code) ComputeCallGraph(srcDir string) *CallGraphResult {
	result := &CallGraphResult{}

	// 1. Load all packages
	cfg := &packages.Config{
		Mode: packages.LoadAllSyntax,
		Dir:  srcDir,
	}
	initial, err := packages.Load(cfg, "./...")
	if err != nil {
		result.Error = fmt.Sprintf("packages.Load: %v", err)
		return result
	}

	// Check for critical load errors
	var hasPackages bool
	packages.Visit(initial, func(pkg *packages.Package) bool {
		hasPackages = true
		return true
	}, nil)
	if !hasPackages {
		result.Error = "no packages found"
		return result
	}

	if len(initial) > 0 && initial[0].Module != nil {
		result.Module = initial[0].Module.Path
	}

	// 2. Build SSA
	prog, _ := ssautil.AllPackages(initial, ssa.InstantiateGenerics)
	prog.Build()

	// 3. VTA call graph
	allFuncs := ssautil.AllFunctions(prog)
	cg := vta.CallGraph(allFuncs, nil)

	// 4. Extract call edges
	seen := make(map[string]bool)
	callgraph.GraphVisitEdges(cg, func(edge *callgraph.Edge) error {
		caller := edge.Caller.Func
		callee := edge.Callee.Func

		if caller.Package() == nil || callee.Package() == nil {
			return nil
		}

		// Only include edges where at least one end is in our module
		if result.Module != "" {
			callerPkg := caller.Package().Pkg.Path()
			calleePkg := callee.Package().Pkg.Path()
			if !strings.HasPrefix(callerPkg, result.Module) && !strings.HasPrefix(calleePkg, result.Module) {
				return nil
			}
		}

		callerID := funcID(caller)
		calleeID := funcID(callee)
		key := callerID + "->" + calleeID
		if seen[key] {
			return nil
		}
		seen[key] = true

		ce := CallEdge{
			CallerID:   callerID,
			CalleeID:   calleeID,
			CallerName: caller.Name(),
			CalleeName: callee.Name(),
			CallType:   "static",
		}

		if edge.Site != nil {
			pos := edge.Site.Parent().Prog.Fset.Position(edge.Site.Pos())
			if pos.IsValid() {
				ce.File = pos.Filename
				ce.Line = pos.Line
			}
		}

		result.Calls = append(result.Calls, ce)
		return nil
	})

	// 5. Extract implements edges
	var interfaces []*types.Interface
	var interfaceNames []string
	var namedTypes []*types.Named
	var namedTypeNames []string

	for _, pkg := range initial {
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			tn, ok := obj.(*types.TypeName)
			if !ok {
				continue
			}
			named, ok := tn.Type().(*types.Named)
			if !ok {
				continue
			}
			iface, isIface := named.Underlying().(*types.Interface)
			if isIface && iface.NumMethods() > 0 {
				interfaces = append(interfaces, iface)
				interfaceNames = append(interfaceNames, pkg.PkgPath+"."+name)
			} else if !isIface {
				namedTypes = append(namedTypes, named)
				namedTypeNames = append(namedTypeNames, pkg.PkgPath+"."+name)
			}
		}
	}

	implSeen := make(map[string]bool)
	for i, named := range namedTypes {
		for j, iface := range interfaces {
			if types.Implements(named, iface) || types.Implements(types.NewPointer(named), iface) {
				key := namedTypeNames[i] + "->" + interfaceNames[j]
				if implSeen[key] {
					continue
				}
				implSeen[key] = true
				result.Implements = append(result.Implements, ImplementsEdge{
					TypeID:        namedTypeNames[i],
					TypeName:      namedTypeNames[i],
					InterfaceID:   interfaceNames[j],
					InterfaceName: interfaceNames[j],
				})
			}
		}
	}

	return result
}

func funcID(fn *ssa.Function) string {
	if recv := fn.Signature.Recv(); recv != nil {
		typeName := recvType(recv.Type())
		return fmt.Sprintf("%s.(%s).%s", fn.Package().Pkg.Path(), typeName, fn.Name())
	}
	return fmt.Sprintf("%s.%s", fn.Package().Pkg.Path(), fn.Name())
}

func recvType(t types.Type) string {
	switch v := t.(type) {
	case *types.Named:
		return v.Obj().Name()
	case *types.Pointer:
		return "*" + recvType(v.Elem())
	default:
		return t.String()
	}
}
