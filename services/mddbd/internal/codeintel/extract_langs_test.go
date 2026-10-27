package codeintel

import (
	"reflect"
	"testing"
)

func TestExtractLanguages(t *testing.T) {
	cases := []struct {
		lang, src string
		want      Symbols
	}{
		{"go", `package srv

import "fmt"
import (
	"net/http"
	str "strings"
	_ "embed"
)

// helper() in a comment is not a call
type Server struct{}

func (s *Server) Serve(w http.ResponseWriter) {
	fmt.Println("route(x)")
	route(w)
	n := len(str.Fields("a"))
	_ = n
}

func route(w any) { audit() }
`, Symbols{
			Defines: []string{"Serve", "Server", "route"},
			Uses:    []string{"audit"},
			Imports: []string{"embed", "fmt", "net/http", "strings"},
		}},
		{"rust", `use std::collections::{HashMap, HashSet};
pub use crate::db::Store;
mod parser;
pub(crate) mod lexer;
extern crate serde;

/* tokenize() in a comment */
pub struct Token<'a> { text: &'a str }
enum Kind { Word }
trait Visit {}
macro_rules! ensure { () => {} }

fn parse<'a>(src: &'a str) -> Vec<Token<'a>> {
    let c = '(';
    println!("{}", tokenize(src));
    ensure!();
    lex(src).iter().count();
    Vec::new()
}
`, Symbols{
			Defines: []string{"Kind", "Token", "Visit", "ensure", "parse"},
			Uses:    []string{"lex", "tokenize"},
			Imports: []string{"./lexer", "./parser", "crate::db::Store", "serde", "std::collections::{HashMap,HashSet}"},
		}},
		{"java", `package com.example;

import java.util.List;
import java.util.concurrent.*;
import static org.junit.Assert.assertEquals;

public class OrderService extends Base implements Runnable {
    private final List<String> items;

    public OrderService(List<String> items) {
        this.items = items;
    }

    @Override
    public void run() {
        if (items.isEmpty()) { return; }
        Order o = new Order("x(y)");
        validate(o);
    }

    private static <T> List<T> copy(List<T> in) throws IOException {
        return in;
    }
}
`, Symbols{
			Defines: []string{"OrderService", "copy", "run"},
			Uses:    []string{"Order", "validate"},
			Imports: []string{"java/util/List", "java/util/concurrent", "org/junit/Assert"},
		}},
		{"python", `"""Module docstring: render() is not a call."""
import os, json as j
import a.b.c
from . import siblings
from .models import User
from ..core.db import session
from pkg.sub import thing

class Repo(Base):
    async def load(self, key):
        # cache(key) in a comment
        row = fetch(key)
        self.store.put(row)
        print('call(x)')
        return User(row)

def helper():
    return load_all()
`, Symbols{
			Defines: []string{"Repo", "helper", "load"},
			Uses:    []string{"User", "fetch", "load_all"},
			Imports: []string{"../core/db", "./models", "./siblings", "a/b/c", "json", "os", "pkg/sub"},
		}},
		{"ruby", `=begin
block comment with fake(call)
=end
require "json"
require_relative "lib/helper"
require_relative "./util"

module Billing
  class Invoice < Base
    def self.build(attrs)
      new_invoice = Payment.new(attrs)
      validate!(new_invoice) # audit(x)
      total(attrs)
    end

    def paid?
      "paid(x)"
    end
  end
end
`, Symbols{
			Defines: []string{"Billing", "Invoice", "build", "paid?"},
			Uses:    []string{"Payment", "total", "validate"},
			Imports: []string{"./lib/helper", "./util", "json"},
		}},
		{"php", `<?php
namespace App\Http;

use App\Models\User;
use function App\Support\helper;
require_once __DIR__ . '/bootstrap.php';
include 'partials/header.php';
require '/etc/app/config.php';

# render() in a comment
interface Handler {}
trait Logs {}

class UserController implements Handler {
    use Logs;

    public function show(int $id) {
        $user = new \App\Models\User();
        $this->authorize($id);
        $fn = 'callback';
        $fn();
        Cache::remember('k');
        return view('user', ['u' => format_name($user)]);
    }
}

function &ref_helper() { return null; }
`, Symbols{
			Defines: []string{"Handler", "Logs", "UserController", "ref_helper", "show"},
			Uses:    []string{"Cache", "User", "format_name", "view"},
			Imports: []string{"./bootstrap.php", "./partials/header.php", "/etc/app/config.php", "App\\Models\\User", "App\\Support\\helper", "Logs"},
		}},
	}
	for _, c := range cases {
		t.Run(c.lang, func(t *testing.T) {
			got := ExtractWithLimit(c.lang, c.src, DefaultMaxSymbols)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("\n got %#v\nwant %#v", got, c.want)
			}
		})
	}
}

func TestScrubEdges(t *testing.T) {
	for _, c := range []struct {
		name, src string
		sx        syntax
		want      string
	}{
		{"unterminated block comment", "a /* b", cLike, "a  "},
		{"line comment at the end", "a // b", cLike, "a  "},
		{"unterminated raw string", "a `b", cLike, "a 0"},
		{"raw string over lines", "a `b(\n)` c", cLike, "a 0 c"},
		{"unterminated triple quote", `a """b`, pySyntax, "a 0"},
		{"ruby block comment at the start", "=begin\nx\n=end\ny", rbSyntax, "\n\n \ny"},
		{"rust lifetime and chars", `f<'a>('x', '\n')`, rustSyntax, "f<'a>(0, 0)"},
	} {
		if got := scrub(c.src, c.sx, true); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if pyRelative("", "a.b") != "a/b" || pyRelative("..", "x") != "../x" {
		t.Error("pyRelative")
	}
	if _, n := firstRune(""); n != 0 {
		t.Error("firstRune of nothing")
	}
}
