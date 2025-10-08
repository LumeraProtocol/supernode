package cascade

// Config contains settings for the cascade service
type Config struct {
	// SupernodeAccountAddress is the on-chain account address of this supernode.
	SupernodeAccountAddress string `mapstructure:"-" json:"-"`

	RqFilesDir string `mapstructure:"rq_files_dir" json:"rq_files_dir,omitempty"`
}
