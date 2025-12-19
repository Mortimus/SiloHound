package docker

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

const (
	BLOODHOUND       = "docker.io/specterops/bloodhound:latest"
	NEO4J            = "docker.io/library/neo4j:4.4"
	POSTGRESQL       = "docker.io/library/postgres:16"
	PSQLFOLDER       = "bloodhound-data/postgresql"
	NEO4JFOLDER      = "bloodhound-data/neo4j"
	BH_SUCC_START    = "Server started successfully"
	PSQL_SUCC_START  = "database system is ready to accept connections"
	NEO4J_SUCC_START = "Remote interface available"
)

type Manager struct {
	cli *client.Client
	ctx context.Context
}

func NewManager(ctx context.Context) (*Manager, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &Manager{cli: cli, ctx: ctx}, nil
}

func (m *Manager) Close() error {
	return m.cli.Close()
}

func (m *Manager) EnsureNetwork(projectName string) (string, error) {
	netName := fmt.Sprintf("SiloHound_%s_Network", projectName)
	// Check if network exists
	networks, err := m.cli.NetworkList(m.ctx, network.ListOptions{})
	if err != nil {
		return "", err
	}
	for _, n := range networks {
		if n.Name == netName {
			return netName, nil // Exists
		}
	}

	_, err = m.cli.NetworkCreate(m.ctx, netName, network.CreateOptions{
		Driver: "bridge",
	})
	return netName, err
}

func (m *Manager) PullImage(imageName string) error {
	reader, err := m.cli.ImagePull(m.ctx, imageName, image.PullOptions{})
	if err != nil {
		return err
	}
	defer reader.Close()
	io.Copy(io.Discard, reader)
	return nil
}

func (m *Manager) ImageExists(imageName string) (bool, error) {
	images, err := m.cli.ImageList(m.ctx, image.ListOptions{})
	if err != nil {
		return false, err
	}

	for _, img := range images {
		for _, tag := range img.RepoTags {
			if tag == imageName {
				return true, nil
			}
		}
	}
	return false, nil
}

func (m *Manager) SpawnPostgres(projectName, wd, netName string) (string, error) {
	mountPath := filepath.Join(wd, PSQLFOLDER)
	containerName := fmt.Sprintf("SiloHound_%s_PSQL", projectName)

	config := &container.Config{
		Image: POSTGRESQL,
		Env: []string{
			"PGUSER=bloodhound",
			"POSTGRES_USER=bloodhound",
			"POSTGRES_PASSWORD=bloodhoundcommunityedition",
			"POSTGRES_DB=bloodhound",
		},
	}
	hostConfig := &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeBind,
				Source: mountPath,
				Target: "/var/lib/postgresql/data",
			},
		},
		AutoRemove: true,
	}
	networkingConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			netName: {Aliases: []string{"app-db"}},
		},
	}

	return m.runContainer(containerName, config, hostConfig, networkingConfig, PSQL_SUCC_START)
}

func (m *Manager) FixPermissions(hostPath string, uid, gid int) error {
	// Use Postgres image as a 'toolbox' since we expect it to be present for the project
	// Mount hostPath to /data and chown it.

	// Config
	config := &container.Config{
		Image: POSTGRESQL,
		User:  "root", // Run as root to choke permissions
		Cmd:   []string{"chown", "-R", fmt.Sprintf("%d:%d", uid, gid), "/data"},
	}

	// Host Config
	hostConfig := &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeBind,
				Source: hostPath,
				Target: "/data",
			},
		},
		AutoRemove: true,
	}

	resp, err := m.cli.ContainerCreate(m.ctx, config, hostConfig, nil, nil, "")
	if err != nil {
		return fmt.Errorf("failed to create permission fixer container: %w", err)
	}

	if err := m.cli.ContainerStart(m.ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start permission fixer container: %w", err)
	}

	// Wait for it to finish
	statusCh, errCh := m.cli.ContainerWait(m.ctx, resp.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("error waiting for fixer: %w", err)
		}
	case <-statusCh:
		// Done
	}

	return nil
}

func (m *Manager) SpawnNeo4j(projectName, wd, netName string) (string, error) {
	mountPath := filepath.Join(wd, NEO4JFOLDER)
	containerName := fmt.Sprintf("SiloHound_%s_Neo4j", projectName)

	config := &container.Config{
		Image: NEO4J,
		Env: []string{
			"NEO4J_AUTH=neo4j/bloodhoundcommunityedition",
			"NEO4J_labs_plugins=[\"apoc\"]",
			"NEO4J_apoc_export_file_enabled=true",
			"NEO4J_apoc_import_file_enabled=true",
			"NEO4J_apoc_import_file_use__neo4j__config=false",
			"NEO4J_dbms_security_procedures_unrestricted=apoc.*",
		},
		ExposedPorts: nat.PortSet{
			"7474/tcp": struct{}{},
			"7687/tcp": struct{}{},
		},
	}
	hostConfig := &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeBind,
				Source: mountPath,
				Target: "/data",
			},
		},
		PortBindings: nat.PortMap{
			"7474/tcp": []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: "7474"}},
			"7687/tcp": []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: "7687"}},
		},
		AutoRemove: true,
	}
	networkingConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			netName: {Aliases: []string{"graph-db"}},
		},
	}

	return m.runContainer(containerName, config, hostConfig, networkingConfig, NEO4J_SUCC_START)
}

func (m *Manager) SpawnBloodhound(projectName, netName, adminName, adminPass string) (string, error) {
	containerName := fmt.Sprintf("SiloHound_%s_BH", projectName)
	config := &container.Config{
		Image: BLOODHOUND,
		Env: []string{
			"bhe_database_connection=user=bloodhound password=bloodhoundcommunityedition dbname=bloodhound host=app-db",
			"bhe_neo4j_connection=neo4j://neo4j:bloodhoundcommunityedition@graph-db:7687/",
			fmt.Sprintf("bhe_default_admin_principal_name=%s", adminName),
			fmt.Sprintf("bhe_default_admin_password=%s", adminPass),
		},
		ExposedPorts: nat.PortSet{
			"8080/tcp": struct{}{},
		},
	}
	hostConfig := &container.HostConfig{
		PortBindings: nat.PortMap{
			"8080/tcp": []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: "8181"}},
		},
		AutoRemove: true,
	}
	networkingConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			netName: {Aliases: []string{"bloodhound"}},
		},
	}

	return m.runContainer(containerName, config, hostConfig, networkingConfig, BH_SUCC_START)
}

func (m *Manager) StopProjectContainers(projectName string) error {
	prefix := fmt.Sprintf("SiloHound_%s_", projectName)
	return m.stopContainersByPrefix(prefix)
}

func (m *Manager) stopContainersByPrefix(prefix string) error {
	containers, err := m.cli.ContainerList(m.ctx, container.ListOptions{All: true})
	if err != nil {
		return err
	}

	for _, c := range containers {
		for _, n := range c.Names {
			// Container names from API start with /
			name := strings.TrimPrefix(n, "/")
			if strings.HasPrefix(name, prefix) {
				fmt.Printf("Stopping container %s...\n", name)
				timeout := 10
				m.cli.ContainerStop(m.ctx, c.ID, container.StopOptions{Timeout: &timeout})
				m.cli.ContainerRemove(m.ctx, c.ID, container.RemoveOptions{Force: true})
			}
		}
	}
	return nil
}

func (m *Manager) IsRunning(projectName string) (bool, error) {
	prefix := fmt.Sprintf("SiloHound_%s_", projectName)
	containers, err := m.cli.ContainerList(m.ctx, container.ListOptions{}) // Only running
	if err != nil {
		return false, err
	}
	for _, c := range containers {
		for _, n := range c.Names {
			if strings.HasPrefix(strings.TrimPrefix(n, "/"), prefix) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (m *Manager) runContainer(name string, config *container.Config, hostConfig *container.HostConfig, netConfig *network.NetworkingConfig, successLog string) (string, error) {
	// cleanup existing
	m.StopContainer(name)

	resp, err := m.cli.ContainerCreate(m.ctx, config, hostConfig, netConfig, nil, name)
	if err != nil {
		return "", err
	}

	if err := m.cli.ContainerStart(m.ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", err
	}

	fmt.Printf("Started %s (%s). Waiting for readiness...\n", name, resp.ID[:12])

	if err := m.WaitUntilReady(resp.ID, successLog); err != nil {
		return "", fmt.Errorf("container %s failed to become ready: %v", name, err)
	}
	return resp.ID, nil
}

func (m *Manager) StopContainer(nameOrID string) error {
	// Try to find container by name or ID
	containers, err := m.cli.ContainerList(m.ctx, container.ListOptions{All: true})
	if err != nil {
		return err
	}

	for _, c := range containers {
		for _, n := range c.Names {
			if strings.TrimPrefix(n, "/") == nameOrID || c.ID == nameOrID {
				// Stop it
				timeout := 10 // seconds
				m.cli.ContainerStop(m.ctx, c.ID, container.StopOptions{Timeout: &timeout})
				m.cli.ContainerRemove(m.ctx, c.ID, container.RemoveOptions{Force: true})
				return nil
			}
		}
	}
	return nil
}

func (m *Manager) WaitUntilReady(containerID, successLog string) error {
	out, err := m.cli.ContainerLogs(m.ctx, containerID, container.LogsOptions{ShowStdout: true, ShowStderr: true, Follow: true})
	if err != nil {
		return err
	}
	defer out.Close()

	buf := make([]byte, 1024)
	for {
		n, err := out.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			if strings.Contains(chunk, successLog) {
				return nil
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
	}
	return nil
}

func (m *Manager) Exec(containerID string, cmd []string) error {
	cfg := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}

	resp, err := m.cli.ContainerExecCreate(m.ctx, containerID, cfg)
	if err != nil {
		return err
	}

	err = m.cli.ContainerExecStart(m.ctx, resp.ID, container.ExecStartOptions{})
	if err != nil {
		return err
	}

	return nil
}
