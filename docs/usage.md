# wisp

## What it is

wisp turns a unit of work into a running workspace: one work item, one tmux session. It finds the item (locally or on GitLab), builds any git worktrees it needs, writes a context file for your agent, and drops you into a tmux session named after the item.

wisp has no list of supported agents. The `program:` setting is just a command line that tmux runs with the context appended as one argument, so anything that takes a prompt as an argument works.

## Install and run it

You need `tmux` and `git`. Then install with one of:

```
go install github.com/fisherrjd/wisp@latest      # any machine with Go
nix profile install github:fisherrjd/wisp        # any machine with nix
```

Make a directory of repos into a workspace, then open the picker:

```
wisp ws new mine ~/code        # a directory holding your repos becomes a workspace
wisp                           # the picker; answer its one question with `default`
```

In the picker press `ctrl-n`, type a name, and press `enter`. A session opens with an agent window briefed on the item and a worktree per repo.

## Use it

```
wisp                  go to the picker session, creating it if needed
wisp open <item>      open an item directly
wisp new <name|url>   make an item: a name, or a GitLab link
wisp next / wisp prev cycle between item sessions
wisp done <item>      close an item out (-m <line> writes a note)
wisp kill <item>      kill an item's session
wisp ls               list live sessions, every workspace
wisp workflow         show the workflow in effect, and where each key came from
wisp version          print the version
```

Every command, flag and error message is in [Commands](commands.md). The picker's keys are in [The picker](picker.md), and [Sessions and worktrees](sessions.md) explains what an open item gives you. Nothing wisp does destroys work: killing a session leaves worktrees and branches in place.

To work on several machines, see [Remote workspaces](remote-workspaces.md).

## Configuration

Settings live in `<workspace>/.wisp.yaml`, which wins over `~/.config/wisp/config.yaml`; environment variables such as `WISP_WORKSPACE` and `WISP_PROGRAM` win over both. `wisp ws new` creates a commented `.wisp.yaml` for you.

See [Configuration](configuration.md) for every key and its default, and [Workflows](workflows.md) to change what runs when an item opens.
