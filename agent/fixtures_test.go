package agent

const testLocalDir = ".agent"

func defaultConfig() *Config {
	return &Config{
		BaseURL:          "https://api.openai.com/v1",
		Model:            "deepseek-v4-flash",
		Temperature:      0.7,
		ApiProtocol:      "responses",
		SessionMode:      "auto",
		UserAgent:        "battlelab/dev",
		LocalDir:         testLocalDir,
		DataDir:          "/tmp/battlelab-test-data",
		ConfigPath:       "/tmp/battlelab-test-config.yaml",
		AutoArchive:      true,
		ArchiveThreshold: DefaultArchiveThreshold,
		ArchiveKeep:      DefaultArchiveKeep,
	}
}
