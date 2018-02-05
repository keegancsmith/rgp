# ripgrep plus

`rgp` wraps [ripgrep](https://github.com/BurntSushi/ripgrep) to add google
like queries to patterns. You can now do queries like
```sh
$ rgp repo:myservice -file:test.go io.Writer case:yes
```

and it will search across all your local code. Just like sourcegraph, zoekt or
chromium codesearch does.

## Demo

[![asciicast](https://asciinema.org/a/161083.png)](https://asciinema.org/a/161083)

## Installation

Ensure `ripgrep` is on your path https://github.com/BurntSushi/ripgrep#installation

```sh
# Install with go toolchain
go get github.com/keegancsmith/rgp

# Add to your .bashrc, for now just eval locally. These are folder roots
# contains clones of repos. Similiar to PATH or GOPATH, etc.
export SRCPATH=$HOME/src:$HOME/go/src
```

## Future

This is an early release, so bugs, perf and code cleanliness will come.

I want to use this in my editor to quickly jump between projects, files,
search results all from a unified interface. Initially this would likely be an
emacs package (via ivy). But vscode would also be interesting to support.

## Why

- *Why use google like patterns?* I find it much more natural to build the
  pattern this way, vs having to jump around previous commands to insert the
  correct flags. This tool also provides tooling around quickly picking a repo
  to search / searching across multiple repos.
  
- *Why SRCPATH?* Hopefully this can become a standard for other tooling to
  start using (any tool that needs to discover where you keep your code
  locally, eg IDEs). It follows the same pattern used by many other unix
  tools.
  
- *Why go?* I am proficient in it. I'll likely learn some rust so it
  potentially better interoperates with ripgrep. Or I'll embed a go tool which
  is also fast at searching like pt.
