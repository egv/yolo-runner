package git

type Runner interface {
	Run(name string, args ...string) (string, error)
}

type Adapter struct {
	runner Runner
}

func New(runner Runner) *Adapter {
	return &Adapter{runner: runner}
}

func (a *Adapter) IsDirty() (bool, error) {
	output, err := a.runner.Run("git", "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return output != "", nil
}

func (a *Adapter) StatusPorcelain() (string, error) {
	return a.runner.Run("git", "status", "--porcelain")
}

func (a *Adapter) RestoreAll() error {
	_, err := a.runner.Run("git", "restore", ".")
	return err
}

func (a *Adapter) CleanAll() error {
	_, err := a.runner.Run("git", "clean", "-fd")
	return err
}

func (a *Adapter) AddAll() error {
	_, err := a.runner.Run("git", "add", ".")
	return err
}

func (a *Adapter) Commit(message string) error {
	_, err := a.runner.Run("git", "commit", "-m", message)
	return err
}

func (a *Adapter) RevParseHead() (string, error) {
	output, err := a.runner.Run("git", "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return output, nil
}
