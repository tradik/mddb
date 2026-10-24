package codeintel

import "testing"

// #319: C and assembly, for a codebase loaded one document per file.

// The example from the blog post: a static prototype, a definition with the
// return type on its own line, calls, and both include forms.
const requestC = `
#include <net/conn.h>
#include <util/buf.h>
#include "request_internal.h"

static int parse_header(struct conn *c, struct buf *b);

int
parse_request(struct conn *c)
{
	struct buf *b = buf_alloc(c->rx_len);

	if (parse_header(c, b) != 0)
		return (conn_close(c, ERR_BAD_REQUEST));
	return (dispatch(c, b));
}
`

func TestCExtractsTheBlogExample(t *testing.T) {
	s := ExtractWithLimit("c", requestC, 0)
	hasAll(t, s.Defines, "parse_header", "parse_request")
	hasAll(t, s.Uses, "buf_alloc", "conn_close", "dispatch")
	hasNone(t, s.Uses, "if", "return", "parse_header", "parse_request", "ERR_BAD_REQUEST")
	hasAll(t, s.Imports, "<net/conn.h>", "<util/buf.h>", "request_internal.h")
}

func TestCDefinitionsTypesMacrosAndGlobals(t *testing.T) {
	src := `
#define	BUF_FLAGS_ALL 0xffu
#define buf_assert_owned(b) \
	ASSERT(1, ("owned"))
struct buf {
	int flags;
};
union u_tag { int a; float b; };
enum buf_state { BUF_A, BUF_B };
typedef struct buf *buf_t;
typedef int (*buf_reader_fn)(int);
typedef struct { int x; } anon_t;
static struct lock buf_pool_lock;
int buf_count = 0, buf_other[4];
extern int not_here;
static int helper(void) __unused;
static __inline int
inline_one(int a)
{
	return (a);
}
void *
buf_alloc(size_t size, int flags)
{
	struct buf *m = NULL;
	return (m);
}
`
	s := ExtractWithLimit("c", src, 0)
	hasAll(t, s.Defines,
		"BUF_FLAGS_ALL", "buf_assert_owned", // macros
		"buf", "u_tag", "buf_state", // tags
		"buf_t", "buf_reader_fn", "anon_t", // typedefs
		"buf_pool_lock", "buf_count", "buf_other", // globals
		"helper", "inline_one", "buf_alloc", // functions
	)
	hasNone(t, s.Defines, "not_here", "__unused", "flags", "m", "a", "x", "int", "struct")
}

// A call is a use only when the callee is not this file's own and is not
// language syntax; member calls through a pointer are not global symbols.
func TestCUsesAreExternalCallsOnly(t *testing.T) {
	src := `
static void local(void) { }
void
f(struct conn *c)
{
	local();
	c->ops->send(c);
	obj.method(1);
	lock_acquire(&c->lock);
	x = sizeof(struct foo) + (int)(y);
	while (cond(z)) { switch (k) { } }
	ASSERT(x != 0, ("msg %d", g(1)));
}
`
	s := ExtractWithLimit("c", src, 0)
	hasAll(t, s.Uses, "lock_acquire", "cond", "ASSERT", "g")
	hasNone(t, s.Uses, "local", "send", "method", "sizeof", "while", "switch", "int", "f")
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
#include <arch/asm.h>
#include "offsets.inc"
	.text
/* ENTRY(commented) */
ENTRY(memcpy_fast)
	call	memcpy_slow
	jmp	done
END(memcpy_fast)
	.globl	fill_words
fill_words:
	callq	*%rax
	bl	arm_helper
`
	s := ExtractWithLimit("asm", src, 0)
	hasAll(t, s.Defines, "memcpy_fast", "fill_words")
	hasNone(t, s.Defines, "commented")
	hasAll(t, s.Uses, "memcpy_slow", "arm_helper")
	hasAll(t, s.Imports, "<arch/asm.h>", "offsets.inc")
}

// Macros invoked at file scope are not functions: on a large real codebase
// the first run reported list-head and registration macros as definitions.
func TestCFileScopeMacrosAreNotFunctions(t *testing.T) {
	src := `
static LIST_HEAD(, conn) idle_conns;
static REGISTER_HANDLER(net, status, FLAG_RW, 0, "status");
QUEUE_HEAD(, job) pending_jobs = QUEUE_HEAD_INITIALIZER(pending_jobs);
DEFINE_COMMAND(status, cmd_status)
{
	print_status(addr);
}
`
	s := ExtractWithLimit("c", src, 0)
	hasAll(t, s.Defines, "idle_conns")
	hasNone(t, s.Defines, "LIST_HEAD", "REGISTER_HANDLER", "DEFINE_COMMAND", "QUEUE_HEAD")
	hasAll(t, s.Uses, "print_status")
}

// Malformed and truncated input must still return, not loop or panic.
func TestCSurvivesBrokenInput(t *testing.T) {
	for _, src := range []string{"", "int f(", "}}}", "{{{", "/* never closed", "\"open", "#define", "#include", "typedef ;", "struct {"} {
		_ = ExtractWithLimit("c", src, 0)
		_ = ExtractWithLimit("asm", src, 0)
	}
}
