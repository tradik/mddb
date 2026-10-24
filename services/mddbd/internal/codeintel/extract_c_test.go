package codeintel

import "testing"

// #319: C and assembly, for a kernel tree loaded one document per file.

// The example from the issue, verbatim in shape: a static prototype, a
// definition in FreeBSD style (return type on its own line), calls, and
// angle-bracket includes.
const kernExec = `
#include <sys/param.h>
#include <sys/proc.h>
#include "opt_ktrace.h"

static int do_execve(struct thread *td, struct image_args *args, ...);

int
sys_execve(struct thread *td, struct execve_args *uap)
{
	int error;

	error = exec_copyin_args(&args, uap->fname, uap->argv, uap->envv);
	if (error == 0)
		error = kern_execve(td, &args, NULL, oldvmspace);
	return (error);
}
`

func TestCExtractsTheIssueExample(t *testing.T) {
	s := ExtractWithLimit("c", kernExec, 0)
	hasAll(t, s.Defines, "do_execve", "sys_execve")
	hasAll(t, s.Uses, "exec_copyin_args", "kern_execve")
	hasNone(t, s.Uses, "if", "return", "sys_execve", "do_execve")
	hasAll(t, s.Imports, "<sys/param.h>", "<sys/proc.h>", "opt_ktrace.h")
}

func TestCDefinitionsTypesMacrosAndGlobals(t *testing.T) {
	src := `
#define	VM_PAGE_BITS_ALL 0xffu
#define vm_page_assert_locked(m) \
	KASSERT(1, ("locked"))
struct vm_page {
	int flags;
};
union u_tag { int a; float b; };
enum vm_state { VM_A, VM_B };
typedef struct vm_page *vm_page_t;
typedef int (*vm_pager_fn)(int);
typedef struct { int x; } anon_t;
static struct mtx vm_page_queue_lock;
int vm_page_count = 0, vm_other[4];
extern int not_here;
static int helper(void) __unused;
static __inline int
inline_one(int a)
{
	return (a);
}
void *
vm_page_alloc(vm_object_t object, int req)
{
	struct vm_page *m = NULL;
	return (m);
}
`
	s := ExtractWithLimit("c", src, 0)
	hasAll(t, s.Defines,
		"VM_PAGE_BITS_ALL", "vm_page_assert_locked", // macros
		"vm_page", "u_tag", "vm_state", // tags
		"vm_page_t", "vm_pager_fn", "anon_t", // typedefs
		"vm_page_queue_lock", "vm_page_count", "vm_other", // globals
		"helper", "inline_one", "vm_page_alloc", // functions
	)
	hasNone(t, s.Defines, "not_here", "__unused", "flags", "m", "a", "x", "int", "struct")
}

// A call is a use only when the callee is not this file's own and is not
// language syntax; member calls through a pointer are not global symbols.
func TestCUsesAreExternalCallsOnly(t *testing.T) {
	src := `
static void local(void) { }
void
f(struct softc *sc)
{
	local();
	sc->ops->start(sc);
	obj.method(1);
	mtx_lock(&sc->mtx);
	x = sizeof(struct foo) + (int)(y);
	while (cond(z)) { switch (k) { } }
	KASSERT(x != 0, ("msg %d", g(1)));
}
`
	s := ExtractWithLimit("c", src, 0)
	hasAll(t, s.Uses, "mtx_lock", "cond", "KASSERT", "g")
	hasNone(t, s.Uses, "local", "start", "method", "sizeof", "while", "switch", "int", "f")
}

// Comments and literals hide braces and parentheses that would otherwise
// throw the depth count, and must not produce symbols.
func TestCCommentsAndStringsAreNotCode(t *testing.T) {
	src := `
/* static int ghost(void) { } */
// int ghost2(void) { }
static const char *msg = "not_a_call( { ";
char c = '{';
int
real(void)
{
	printf("%s ) }", msg); /* } */
	return (0);
}
int after(void) { return 1; }
`
	s := ExtractWithLimit("c", src, 0)
	hasAll(t, s.Defines, "real", "after", "msg", "c")
	hasNone(t, s.Defines, "ghost", "ghost2")
	hasAll(t, s.Uses, "printf")
	hasNone(t, s.Uses, "not_a_call")
}

func TestCPlusPlusUsesTheCExtractor(t *testing.T) {
	s := ExtractWithLimit("cpp", "#include <vector>\nint run(int a) { return go(a); }\n", 0)
	hasAll(t, s.Defines, "run")
	hasAll(t, s.Uses, "go")
	hasAll(t, s.Imports, "<vector>")
}

func TestAssemblyEntriesCallsAndIncludes(t *testing.T) {
	src := `
#include <machine/asmacros.h>
#include "assym.inc"
	.text
/* ENTRY(commented) */
ENTRY(copyin)
	call	copyin_nosmap
	jmp	done
END(copyin)
	.globl	fillw
fillw:
	callq	*%rax
	bl	arm_helper
`
	s := ExtractWithLimit("asm", src, 0)
	hasAll(t, s.Defines, "copyin", "fillw")
	hasNone(t, s.Defines, "commented")
	hasAll(t, s.Uses, "copyin_nosmap", "arm_helper")
	hasAll(t, s.Imports, "<machine/asmacros.h>", "assym.inc")
}

// Kernel macros at file scope are not functions: found on the FreeBSD tree,
// where SLIST_HEAD and SYSCTL_NODE came out as definitions.
func TestCFileScopeMacrosAreNotFunctions(t *testing.T) {
	src := `
static SLIST_HEAD(, exec_args_kva) exec_args_kva_freelist;
static SYSCTL_NODE(_vm, OID_AUTO, stats, CTLFLAG_RW, 0, "VM stats");
TAILQ_HEAD(, proc) allproc_list = TAILQ_HEAD_INITIALIZER(allproc_list);
DB_SHOW_COMMAND(vmpage, db_show_vmpage)
{
	db_printf("%p", addr);
}
`
	s := ExtractWithLimit("c", src, 0)
	hasAll(t, s.Defines, "exec_args_kva_freelist")
	hasNone(t, s.Defines, "SLIST_HEAD", "SYSCTL_NODE", "DB_SHOW_COMMAND", "TAILQ_HEAD")
	hasAll(t, s.Uses, "db_printf")
}

// Malformed and truncated input must still return, not loop or panic.
func TestCSurvivesBrokenInput(t *testing.T) {
	for _, src := range []string{"", "int f(", "}}}", "{{{", "/* never closed", "\"open", "#define", "#include", "typedef ;", "struct {"} {
		_ = ExtractWithLimit("c", src, 0)
		_ = ExtractWithLimit("asm", src, 0)
	}
}
