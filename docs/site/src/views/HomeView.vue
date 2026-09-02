<script setup lang="ts">
import { RouterLink } from 'vue-router'
import PageGrid from '@/components/PageGrid.vue'
import Terminal from '@/components/Terminal.vue'

// The five nouns. Everything else on the site follows from them, so they are
// the first thing under the hero and they are cards rather than prose.
const nouns = [
  {
    name: 'System',
    body: 'A machine. This one, or one you reach over ssh. ',
    em: 'It owns its workspaces and answers for them',
    tail: '; nothing here keeps a copy of what it holds.',
  },
  {
    name: 'Workspace',
    body: 'The container. Every repo checkout, docs/, .worktrees/ and the vault sit side by side inside it. ',
    em: 'Agents start here, so one cwd sees all of them.',
    tail: '',
  },
  {
    name: 'Item',
    body: 'A folder in the vault. Its stable identity is only <repo>/<iid>, because ',
    em: 'slugs drift',
    tail: ' between what you typed and what GitLab derives from the title.',
  },
  {
    name: 'Worktree',
    body: '',
    em: 'A cache, deliberately.',
    tail: ' Branches are the real state. Delete a worktree and reopening the item reprovisions it.',
  },
  {
    name: 'Session',
    body: 'A tmux session tagged @wisp_item. The agent window at the workspace root, ',
    em: 'one shell per worktree',
    tail: ' for builds and dev servers.',
  },
]

const levels = [
  {
    level: 'session',
    move: 'next / prev, or the list',
    why: 'Never leaves the workspace, so what is running elsewhere cannot get in the way of flipping between the work in front of you.',
  },
  {
    level: 'workspace',
    move: 'ctrl-w for the tree, hop for the blind step',
    why: 'Its own vault, its own GitLab group, its own sessions. Two workspaces holding the same slug get two sessions, not one.',
  },
  {
    level: 'system',
    move: '← → in that tree',
    why: 'A whole machine at a time, because up and down would otherwise mean several presses to get past one.',
  },
]

const ladder = [
  { mark: '+', cls: 'g-remote', name: 'gitlab', gloss: 'on GitLab, nothing local' },
  { mark: '○', cls: 'g-folder', name: 'folder', gloss: 'a vault folder, no session' },
  { mark: '●', cls: 'g-live', name: 'live', gloss: 'a session is running' },
  { mark: '?', cls: 'g-attn', name: 'needs input', gloss: 'the agent is waiting on you' },
  { mark: '✓', cls: 'g-faint', name: 'done', gloss: 'closed out' },
]

const requirements = [
  { bin: 'tmux', need: 'Everything.' },
  { bin: 'ssh', need: 'Remote workspaces. Purely local use never invokes it.' },
  {
    bin: 'glab',
    need: 'Optional. Without it the GitLab source is empty and pasting a link fails; everything else is unaffected.',
  },
  { bin: 'git', need: 'Not invoked by wisp itself. Your provisioning script needs it.' },
]
</script>

<template>
  <div>
    <section class="rise-in pt-14 pb-10">
      <h1
        class="mb-2.5 text-[clamp(2.1rem,6vw,3.4rem)] leading-[1.05] font-semibold tracking-[-0.035em] text-balance"
      >
        One work item, one tmux session.
      </h1>
      <p
        class="mb-2 max-w-[60ch] text-[clamp(1.05rem,2.2vw,1.3rem)] text-pretty text-muted-foreground"
      >
        wisp turns a unit of work into a running workspace: it finds the item, reprovisions the
        git worktrees it needs, writes a context file for the agent, and drops you into a tmux
        session named after it.
        <strong class="font-semibold text-foreground">Nothing it does destroys work.</strong>
      </p>
      <p
        class="mb-8 max-w-[60ch] text-[clamp(1.05rem,2.2vw,1.3rem)] text-pretty text-muted-foreground"
      >
        <strong class="font-semibold text-foreground"
          >And it has no concept of a supported agent.</strong
        >
        The command in the session is a command line you write, so there is no adapter layer and
        no list yours has to be on.
      </p>

      <!-- 144 columns, laid out by scripts/hero-transcript.mjs against the
           arithmetic in internal/ui/view.go: a 57-column list pane with its
           border in column 57, an 87-column preview, and a footer rule across
           all of it. Regenerate rather than editing the spaces.

           The page's max width is set to this window rather than the other way
           around, in App.vue. A terminal narrower than the page it sits on
           looks undersized; wider, and it looks like it escaped. -->
      <Terminal title='1: wisp-airbook:1:wisp - "airbook"' glow>
<pre class="term-pre"><span class="g-acc">› </span><span class="g-acc">▏</span>                                                                                                                   <span class="g-acc-b">airbook</span><span class="g-attn"> ?1</span><span class="g-faint"> · </span><span class="g-faint">eldo</span><span class="g-live"> ●3</span>   <span class="g-faint">8/8</span>
  <span class="g-live">●</span> <span class="g-faint">ledger-service/</span><span class="g-soft">318-audit-trail</span>                      <span class="g-rule">│</span>  <span class="g-soft">● Backfill streams by day now, and the batch size is a flag.</span>
<span class="g-acc">▌ </span><span class="g-attn">?</span> <span class="g-faint">ledger-service/</span><span class="g-ink-b">327-backfill</span>                         <span class="g-rule">│</span>  
  <span class="g-folder">○</span> <span class="g-faint">ledger-service/</span><span class="g-soft">331-fx-rounding</span>                      <span class="g-rule">│</span>  <span class="g-soft">  A run that died halfway restarted from zero. The resume path has a test now.</span>
  <span class="g-folder">○</span> <span class="g-faint">_adhoc/</span><span class="g-soft">vq-workspace</span>                                 <span class="g-rule">│</span>  
  <span class="g-remote">+</span> <span class="g-faint">payments-api/</span><span class="g-soft">1042-idempotency</span>                       <span class="g-rule">│</span>  <span class="g-soft">Run the migration against staging?</span>
<span class="g-rule">────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────</span>
<span class="g-live">●</span><span class="g-soft"> live</span>   <span class="g-attn">?</span><span class="g-soft"> needs input</span>   <span class="g-folder">○</span><span class="g-soft"> folder</span>   <span class="g-remote">+</span><span class="g-soft"> gitlab</span>                                      <span class="g-soft">enter open   ctrl-n new   ctrl-d done   ctrl-g keys   esc quit</span></pre>
      </Terminal>
    </section>

    <section id="model" class="scroll-mt-20 pt-4">
      <h2 class="mb-1.5 text-2xl font-semibold tracking-[-0.025em] text-balance">The model</h2>
      <p class="mb-6 max-w-[68ch] text-pretty text-muted-foreground">
        Five nouns, and everything else follows from them.
      </p>
      <div class="grid gap-3.5 sm:grid-cols-2 lg:grid-cols-3">
        <div v-for="noun in nouns" :key="noun.name" class="rounded-lg border bg-card px-4.5 py-4">
          <h3
            class="mb-1 text-[0.7rem] font-bold tracking-[0.1em] text-primary uppercase"
          >
            {{ noun.name }}
          </h3>
          <p class="text-sm text-muted-foreground">
            {{ noun.body }}<span class="font-medium text-foreground">{{ noun.em }}</span
            >{{ noun.tail }}
          </p>
        </div>
      </div>
      <p
        class="my-5 max-w-[66ch] border-l-2 border-primary/55 pl-4 text-pretty text-muted-foreground"
      >
        Killing a session leaves worktrees and branches. Deleting a worktree leaves the branch.
        Closing an item out leaves every file it holds.
        <strong class="font-semibold text-foreground"
          >There is no command that removes your work.</strong
        >
      </p>
    </section>

    <section id="levels" class="mt-14 scroll-mt-20">
      <h2 class="mb-1.5 text-2xl font-semibold tracking-[-0.025em] text-balance">Three levels</h2>
      <p class="mb-6 max-w-[68ch] text-pretty text-muted-foreground">
        Systems hold workspaces hold sessions, and wisp moves between all three.
      </p>
      <div class="overflow-x-auto">
        <table class="w-full min-w-[30rem] border-collapse text-[0.88rem]">
          <thead>
            <tr>
              <th
                v-for="head in ['Level', 'Move with', 'Why it is its own level']"
                :key="head"
                class="border-b py-2 pr-3.5 text-left text-[0.68rem] font-semibold tracking-[0.09em] text-muted-foreground uppercase"
              >
                {{ head }}
              </th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in levels" :key="row.level">
              <td class="border-b py-2 pr-3.5 align-top font-semibold whitespace-nowrap">
                {{ row.level }}
              </td>
              <td class="border-b py-2 pr-3.5 align-top font-mono text-[0.82rem]">
                {{ row.move }}
              </td>
              <td class="border-b py-2 pr-3.5 align-top text-muted-foreground">{{ row.why }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p class="mt-4 max-w-[68ch] text-pretty">
        Hopping into a workspace lands on the session you were last in there, not on its picker,
        so a trip out and back is a round trip rather than a reset.
      </p>
    </section>

    <section id="states" class="mt-14 scroll-mt-20">
      <h2 class="mb-1.5 text-2xl font-semibold tracking-[-0.025em] text-balance">
        The state ladder
      </h2>
      <p class="mb-5 max-w-[68ch] text-pretty text-muted-foreground">
        The first four are a ladder. When the same item is found by more than one source the
        highest wins, so a live session always beats the vault folder it came from and the folder
        beats the GitLab row.
      </p>
      <div class="flex flex-wrap gap-2">
        <span
          v-for="state in ladder"
          :key="state.name"
          class="inline-flex items-center gap-2 rounded-full border bg-card py-1.5 pr-3 pl-2.5 text-[0.83rem]"
        >
          <span class="w-[1ch] text-center font-mono text-base" :class="state.cls">{{
            state.mark
          }}</span>
          <span class="text-muted-foreground">
            <b class="font-semibold text-foreground">{{ state.name }}</b> — {{ state.gloss }}
          </span>
        </span>
      </div>
      <p
        class="my-5 max-w-[66ch] border-l-2 border-primary/55 pl-4 text-pretty text-muted-foreground"
      >
        <strong class="font-semibold text-foreground">Done is not a fifth rung.</strong> The
        states are a ladder and merging keeps the highest, so a done item found again as a live
        session would have the flag overwritten by whichever source spoke last. Done is a separate
        axis: it says what you decided, not what the machine observed.
      </p>
      <RouterLink to="/docs/items" class="text-sm">Items, in full →</RouterLink>
    </section>

    <section id="reference" class="mt-14 scroll-mt-20">
      <h2 class="mb-1.5 text-2xl font-semibold tracking-[-0.025em] text-balance">Reference</h2>
      <p class="mb-6 max-w-[68ch] text-pretty text-muted-foreground">
        Every flag, every key, every error message. These pages are
        <span class="font-mono text-[0.9em]">docs/*.md</span> from the repository, rendered here
        rather than retyped.
      </p>
      <PageGrid />
    </section>

    <section id="install" class="mt-14 scroll-mt-20">
      <h2 class="mb-1.5 text-2xl font-semibold tracking-[-0.025em] text-balance">
        Requirements and install
      </h2>
      <div class="my-5 overflow-x-auto">
        <table class="w-full min-w-[30rem] border-collapse text-[0.88rem]">
          <thead>
            <tr>
              <th
                v-for="head in ['Binary', 'Needed for']"
                :key="head"
                class="border-b py-2 pr-3.5 text-left text-[0.68rem] font-semibold tracking-[0.09em] text-muted-foreground uppercase"
              >
                {{ head }}
              </th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in requirements" :key="row.bin">
              <td class="border-b py-2 pr-3.5 align-top">
                <code class="rounded-sm bg-muted px-1.5 py-0.5 font-mono text-[0.85em]">{{
                  row.bin
                }}</code>
              </td>
              <td class="border-b py-2 pr-3.5 align-top text-muted-foreground">{{ row.need }}</td>
            </tr>
          </tbody>
        </table>
      </div>
<pre class="my-5 overflow-x-auto rounded-lg border bg-muted/50 px-4.5 py-3.5 font-mono text-[0.82rem] leading-[1.7]">nix build github:fisherrjd/wisp

# or, on a server you only want the far end on:
go install github.com/fisherrjd/wisp@latest</pre>
      <p class="max-w-[68ch] text-pretty">
        A remote workspace needs wisp on both machines, at versions speaking the same wire; a
        mismatch says so by name and number rather than half-working.
      </p>
    </section>
  </div>
</template>
