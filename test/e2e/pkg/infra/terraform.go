package infra

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/terraform-exec/tfexec"
)

var ErrTerraformStateNotFound = errors.New("Terraform state not found")

// Terraform manager executes Terraform commands.
type Terraform struct {
	tfexec *tfexec.Terraform
	CCIP   string
}

func terraformDir() (string, error) {
	cwd, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, "../..", "terraform"), nil
}

func NewTerraform() (*Terraform, error) {
	dir, err := terraformDir()
	if err != nil {
		return nil, err
	}
	tf, err := tfexec.NewTerraform(dir, "terraform")
	if err != nil {
		return nil, err
	}
	tf.SetStdout(os.Stdout)
	tf.SetStderr(os.Stderr)
	return &Terraform{tfexec: tf}, nil
}

func (tf *Terraform) Init(ctx context.Context) error {
	return tf.tfexec.Init(ctx)
}

func (tf *Terraform) Validate(ctx context.Context) error {
	_, err := tf.tfexec.Validate(ctx)
	return err
}

func (tf *Terraform) Show(ctx context.Context) error {
	_, err := tf.tfexec.Show(ctx)
	return err
}

func (tf *Terraform) planTask(ctx context.Context, varAssignments []string, destroy bool) (*tfexec.VarFileOption, error) {
	// Set plan file.
	tmpPath, err := os.CreateTemp("", "tf-plan")
	if err != nil {
		return nil, err
	}

	// Display plan.
	planFileOpt := tfexec.VarFile(tmpPath.Name())
	planOpts := []tfexec.PlanOption{planFileOpt, tfexec.Destroy(destroy)}
	for _, assignment := range varAssignments {
		planOpts = append(planOpts, tfexec.Var(assignment))
	}
	hasChanged, err := tf.tfexec.Plan(ctx, planOpts...)
	if err != nil {
		return nil, err
	}
	if !hasChanged {
		return nil, nil
	}

	return planFileOpt, nil
}

func (tf *Terraform) Apply(ctx context.Context, varAssignments []string, confirm bool) error {
	planFileOpt, err := tf.planTask(ctx, varAssignments, false)
	if err != nil {
		return err
	}
	if planFileOpt == nil { // no changes
		return nil
	}

	if confirm && !askConfirmation(varAssignments) {
		return nil
	}

	// Apply plan file.
	applyOpts := []tfexec.ApplyOption{planFileOpt}
	for _, assignment := range varAssignments {
		applyOpts = append(applyOpts, tfexec.Var(assignment))
	}
	applyOpts = append(applyOpts, tfexec.Parallelism(200))
	return tf.tfexec.Apply(ctx, applyOpts...)
}

func (tf *Terraform) SetCCIP(ctx context.Context) error {
	// TODO: find a better way to obtain the state.
	// Disable output.
	tf.tfexec.SetStdout(nil)
	tf.tfexec.SetStderr(nil)
	// Get state.
	state, err := tf.tfexec.Show(ctx)
	if err != nil {
		return err
	}
	// Re-enable output.
	tf.tfexec.SetStdout(os.Stdout)
	tf.tfexec.SetStderr(os.Stderr)

	if state == nil || state.Values == nil {
		return ErrTerraformStateNotFound
	}

	ccIPV4Address, ok := state.Values.Outputs["cc_ipv4_address"]
	if !ok {
		return errors.New("cc_ipv4_address not found in terraform output")
	}
	tf.CCIP = ccIPV4Address.Value.(string)

	return nil
}

func (tf *Terraform) Destroy(ctx context.Context, varAssignments []string, _, confirm bool) error {
	planFileOpt, err := tf.planTask(ctx, varAssignments, true)
	if err != nil {
		return err
	}
	if planFileOpt == nil { // no changes
		return nil
	}
	if confirm && !askConfirmation(varAssignments) {
		return nil
	}

	// TODO
	// if exceptCC {
	// Get list of all resources
	// Filter cc
	// map resources to TargetOption
	// }

	opts := []tfexec.DestroyOption{planFileOpt}
	for _, assignment := range varAssignments {
		opts = append(opts, tfexec.Var(assignment))
	}
	opts = append(opts, tfexec.Parallelism(200))
	for i := 0; i < 10; i++ {
		// On the first try, it usually fails with:
		// Error: Error deleting VPC: ... Can not delete VPC with members
		// FIX: it's either a bad definition of resource dependencies or an error in digitalocean_vpc.
		err = tf.tfexec.Destroy(ctx, opts...)
		if err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	return err
}

// Ask for confirmation before applying plan.
func askConfirmation(varAssignments []string) bool {
	fmt.Printf("Variables:\n\t%s\n", strings.Join(varAssignments, "\n\t"))
	fmt.Printf("\nDo you want to perform these actions?\n" +
		"\tTerraform will perform the actions described above.\n" +
		"\tOnly 'yes' will be accepted to approve.\n\n" +
		"\tEnter a value: ")
	var response string
	if _, err := fmt.Scanln(&response); err != nil {
		return false
	}
	return response == "yes"
}
