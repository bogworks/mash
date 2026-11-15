package listset

import (
	"slices"
	"testing"

	"github.com/nats-io/jwt/v2"
)

// policyLines are the policy publish lines of a template: every line of the policy responder in either form.
func policyLines(p jwt.Permissions) []string {
	var out []string
	for _, l := range p.Pub.Allow {
		if isPolicySubject(l) {
			out = append(out, l)
		}
	}
	return out
}

func fooTemplate(t *testing.T, c *Compiled) *jwt.Permissions {
	t.Helper()
	for i := range c.Templates {
		if c.Templates[i].Group == "foo.c" {
			return &c.Templates[i].Permissions
		}
	}
	t.Fatal("no template foo.c")
	return nil
}

func TestPolicySubjectForms(t *testing.T) {
	if got := PolicySubjectForm("foo", PolicyFormAgents); got != "agents.policy.foo" {
		t.Errorf("agents form = %q", got)
	}
	if got := PolicySubjectForm("foo", PolicyFormMash); got != "mash.policy.foo" {
		t.Errorf("mash form = %q", got)
	}
	if DefaultPolicyForm != PolicyFormAgents || PolicySubject("foo") != "agents.policy.foo" {
		t.Errorf("the default form is %q", PolicySubject("foo"))
	}
	if got := PolicySubjects("foo"); !slices.Equal(got, []string{"mash.policy.foo", "agents.policy.foo"}) {
		t.Errorf("PolicySubjects = %v", got)
	}
}

func TestCompileFormEmitsTheLinePerForm(t *testing.T) {
	for form, want := range map[PolicyForm]string{PolicyFormAgents: "agents.policy.foo", PolicyFormMash: "mash.policy.foo"} {
		c, err := CompileForm(at001(), form)
		if err != nil {
			t.Fatalf("form %d: %v", form, err)
		}
		if got := policyLines(*fooTemplate(t, c)); !slices.Equal(got, []string{want}) {
			t.Errorf("form %d: policy lines %v, want %q", form, got, want)
		}
	}
	def, err := Compile(at001())
	if err != nil {
		t.Fatal(err)
	}
	agents, err := CompileForm(at001(), PolicyFormAgents)
	if err != nil {
		t.Fatal(err)
	}
	if DigestCompiled(def) != DigestCompiled(agents) {
		t.Error("Compile is not the default form")
	}
}

func TestCompilePrevious(t *testing.T) {
	ls := at001()
	oldC, _ := CompileForm(ls, PolicyFormAgents)
	newC, _ := CompileForm(ls, PolicyFormMash)
	if DigestCompiled(oldC) == DigestCompiled(newC) {
		t.Fatal("the forms digest alike")
	}
	for name, tc := range map[string]struct {
		signed Digest
		want   *Compiled
	}{
		"signed in the old form": {DigestCompiled(oldC), oldC},
		"signed in the new form": {DigestCompiled(newC), newC},
		"signed as neither":      {Digest{1}, oldC}, // the default-form compile, so the caller's check refuses
	} {
		t.Run(name, func(t *testing.T) {
			got, err := CompilePrevious(ls, tc.signed)
			if err != nil {
				t.Fatal(err)
			}
			if DigestCompiled(got) != DigestCompiled(tc.want) {
				t.Errorf("got the wrong form: %v", policyLines(*fooTemplate(t, got)))
			}
		})
	}
	// with the default flipped, the old form is the alternative
	old := DefaultPolicyForm
	DefaultPolicyForm = PolicyFormMash
	defer func() { DefaultPolicyForm = old }()
	if got, _ := CompilePrevious(ls, DigestCompiled(oldC)); DigestCompiled(got) != DigestCompiled(oldC) {
		t.Error("with the new default, a generation signed in the old form is not found")
	}
	if got, _ := CompilePrevious(ls, Digest{1}); DigestCompiled(got) != DigestCompiled(newC) {
		t.Error("with the new default, no match is not the default-form compile")
	}
}

// The template push (ADR 0129): a resubmission of the list set in force, when it was signed in the other form than the
// default, is the next generation; one signed in the default form is still unchanged.
func TestAcceptResubmissionAcrossPolicyForms(t *testing.T) {
	ls := at001()
	otherC, _ := CompileForm(ls, DefaultPolicyForm.other())
	defC, _ := CompileForm(ls, DefaultPolicyForm)
	ls.Base = 1
	rec, _, err := Accept(Record{Generation: 1, Signed: DigestCompiled(otherC)}, ls, []byte("x"))
	if err != nil {
		t.Fatalf("a resubmission after a form change must be accepted: %v", err)
	}
	if rec.Generation != 2 {
		t.Errorf("generation %d, want 2", rec.Generation)
	}
	if _, _, err := Accept(Record{Generation: 1, Signed: DigestCompiled(defC)}, ls, []byte("x")); !isUnchanged(err) {
		t.Fatalf("same form: want the unchanged refusal, got %v", err)
	}
}

// DryRunSigned shows the template push for a generation signed in the other form.
func TestDryRunSignedShowsPolicyFormPush(t *testing.T) {
	ls := at001()
	otherC, _ := CompileForm(ls, DefaultPolicyForm.other())
	defC, _ := CompileForm(ls, DefaultPolicyForm)
	p := DryRunSigned(&ls, DigestCompiled(otherC), ls, nil)
	if p.Refused {
		t.Fatalf("refused: %v", p.Refusals)
	}
	if len(p.Templates) != len(defC.Templates) || len(p.Templates) == 0 {
		t.Fatalf("templates %v, want a change for each of %d groups", p.Templates, len(defC.Templates))
	}
	for _, tc := range p.Templates {
		if tc.Change != TemplateChanged {
			t.Errorf("%s: %s", tc.Group, tc.Change)
		}
	}
	if p := DryRunSigned(&ls, DigestCompiled(defC), ls, nil); len(p.Templates) != 0 {
		t.Errorf("same form shows %v", p.Templates)
	}
	if p := DryRun(&ls, ls, nil); len(p.Templates) != 0 {
		t.Errorf("DryRun shows %v", p.Templates)
	}
}

func isUnchanged(err error) bool { _, ok := err.(*UnchangedError); return ok }

// Check accepts a template whose fixed policy line is either form, and refuses any other shape.
func TestCheckPolicyLineForms(t *testing.T) {
	mutate := func(f func(p *jwt.Permissions)) error {
		ls := at001()
		c, err := Compile(ls)
		if err != nil {
			t.Fatal(err)
		}
		f(fooTemplate(t, c))
		return Check(ls, c)
	}
	swap := func(from, to string) func(p *jwt.Permissions) {
		return func(p *jwt.Permissions) {
			for i, l := range p.Pub.Allow {
				if l == from {
					p.Pub.Allow[i] = to
				}
			}
		}
	}
	if err := mutate(func(*jwt.Permissions) {}); err != nil {
		t.Errorf("the old line: %v", err)
	}
	if err := mutate(swap("agents.policy.foo", "mash.policy.foo")); err != nil {
		t.Errorf("the new line: %v", err)
	}
	for name, f := range map[string]func(p *jwt.Permissions){
		"neither line": func(p *jwt.Permissions) {
			p.Pub.Allow = slices.DeleteFunc(p.Pub.Allow, isPolicySubject)
		},
		"both lines":                func(p *jwt.Permissions) { p.Pub.Allow.Add("mash.policy.foo") },
		"an extra mash line":        func(p *jwt.Permissions) { p.Pub.Allow.Add("mash.other") },
		"mash.>":                    func(p *jwt.Permissions) { p.Pub.Allow.Add("mash.>") },
		"another range's mash line": swap("agents.policy.foo", "mash.policy.bar"),
		"mash.> for the line":       swap("agents.policy.foo", "mash.>"),
	} {
		t.Run(name, func(t *testing.T) {
			if err := mutate(f); err == nil {
				t.Fatal("Check accepted it")
			}
		})
	}
}
