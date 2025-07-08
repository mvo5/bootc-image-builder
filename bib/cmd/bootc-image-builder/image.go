package main

import (
	"bytes"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"

	"github.com/osbuild/images/pkg/arch"
	"github.com/osbuild/images/pkg/blueprint"
	"github.com/osbuild/images/pkg/container"
	"github.com/osbuild/images/pkg/customizations/anaconda"
	"github.com/osbuild/images/pkg/customizations/kickstart"
	"github.com/osbuild/images/pkg/customizations/users"
	"github.com/osbuild/images/pkg/disk"
	"github.com/osbuild/images/pkg/distro"
	"github.com/osbuild/images/pkg/distro/defs"
	"github.com/osbuild/images/pkg/distrofactory"
	"github.com/osbuild/images/pkg/image"
	"github.com/osbuild/images/pkg/imagefilter"
	"github.com/osbuild/images/pkg/manifest"
	"github.com/osbuild/images/pkg/manifestgen"
	"github.com/osbuild/images/pkg/osbuild"
	"github.com/osbuild/images/pkg/pathpolicy"
	"github.com/osbuild/images/pkg/platform"
	"github.com/osbuild/images/pkg/policies"
	"github.com/osbuild/images/pkg/reporegistry"
	"github.com/osbuild/images/pkg/rpmmd"
	"github.com/osbuild/images/pkg/runner"

	"github.com/osbuild/bootc-image-builder/bib/internal/buildconfig"
	"github.com/osbuild/bootc-image-builder/bib/internal/distrodef"
	"github.com/osbuild/bootc-image-builder/bib/internal/imagetypes"
	"github.com/osbuild/bootc-image-builder/bib/internal/source"
)

// TODO: Auto-detect this from container image metadata
const DEFAULT_SIZE = uint64(10 * GibiByte)

type ManifestConfig struct {
	// OCI image path (without the transport, that is always docker://)
	Imgref      string
	BuildImgref string

	ImageTypes imagetypes.ImageTypes

	// Build config
	Config *buildconfig.BuildConfig

	// CPU architecture of the image
	Architecture arch.Arch

	// The minimum size required for the root fs in order to fit the container
	// contents
	RootfsMinsize uint64

	// Paths to the directory with the distro definitions
	DistroDefPaths []string

	// Extracted information about the source container image
	SourceInfo      *source.Info
	BuildSourceInfo *source.Info

	// RootFSType specifies the filesystem type for the root partition
	RootFSType string

	// use librepo ad the rpm downlaod backend
	UseLibrepo bool
}

/*func Manifest(c *ManifestConfig) (*manifest.Manifest, error) {
	rng := createRand()

	if c.ImageTypes.BuildsISO() {
		return manifestForISO(c, rng)
	}
	return manifestForDiskImage(c, rng)
        }*/

func writeAsYAML(path string, content any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := yaml.NewEncoder(f).Encode(content); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return nil
}

// XXX: move to real YAML for easier editing
var bootcImageTypesContent = `
.common:
  disk_sizes:
    default_required_partition_sizes: &default_required_dir_sizes
      "/": 1_073_741_824     # 1 * datasizes.GiB
      "/usr": 2_147_483_648  # 2 * datasizes.GiB
  partitioning:
    ids:
      - &prep_partition_dosid "41"
      - &filesystem_linux_dosid "83"
      - &fat16_bdosid "06"
    guids:
      - &bios_boot_partition_guid "21686148-6449-6E6F-744E-656564454649"
      - &efi_system_partition_guid "C12A7328-F81F-11D2-BA4B-00A0C93EC93B"
      - &filesystem_data_guid "0FC63DAF-8483-4772-8E79-3D69D8477DE4"
      - &xboot_ldr_partition_guid "BC13C2FF-59E6-4262-A352-B275FD6F7172"
    # static UUIDs for partitions and filesystems
    # NOTE(akoutsou): These are unnecessary and have stuck around since the
    # beginning where (I believe) the goal was to have predictable,
    # reproducible partition tables. They might be removed soon in favour of
    # proper, random UUIDs, with reproducibility being controlled by fixing
    # rng seeds.
    uuids:
      - &bios_boot_partition_uuid "FAC7F1FB-3E8D-4137-A512-961DE09A5549"
      - &root_partition_uuid "6264D520-3FB9-423F-8AB8-7A0A8E3D3562"
      - &data_partition_uuid "CB07C243-BC44-4717-853E-28852021225B"
      - &efi_system_partition_uuid "68B2905B-DF3E-4FB3-80FA-49D1E773AA33"
      - &efi_filesystem_uuid "7B77-95E7"

image_types:
  "qcow2":
    # XXX: images hardcodes adding +".qcow2"
    filename: "disk"
    mime_type: "application/x-qemu-disk"
    bootable: true
    default_size: 10_737_418_240  # 10 GiB
    image_func: "bootc_disk"
    build_pipelines: ["build"]
    payload_pipelines: ["image", "qcow2"]
    exports: ["qcow2"]
    required_partition_sizes: *default_required_dir_sizes
    platforms:
      - arch: "x86_64"
        uefi_vendor: "fedora"
        qcow2_compat: "1.1"
        bootloader: "grub2"
    partition_table:
      x86_64:
        uuid: "D209C89E-EA5E-4FBD-B161-B461CCE297E0"
        type: "gpt"
        partitions:
          - &default_partition_table_part_bios
            size: "1 MiB"
            bootable: true
            type: *bios_boot_partition_guid
            uuid: *bios_boot_partition_uuid
          - &default_partition_table_part_efi
            size: "200 MiB"
            type: *efi_system_partition_guid
            uuid: *efi_system_partition_uuid
            payload_type: "filesystem"
            payload:
              type: vfat
              uuid: *efi_filesystem_uuid
              mountpoint: "/boot/efi"
              label: "ESP"
              fstab_options: "defaults,uid=0,gid=0,umask=077,shortname=winnt"
              fstab_freq: 0
              fstab_passno: 2
          - &default_partition_table_part_boot
            size: "1 GiB"
            type: *filesystem_data_guid
            uuid: *data_partition_uuid
            payload_type: "filesystem"
            payload:
              type: "ext4"
              mountpoint: "/boot"
              label: "boot"
              fstab_options: "defaults"
          - &default_partition_table_part_root
            size: "2 GiB"
            type: *filesystem_data_guid
            uuid: *root_partition_uuid
            payload_type: "filesystem"
            payload: &default_partition_table_part_root_payload
              type: "ext4"
              label: "root"
              mountpoint: "/"
              fstab_options: "defaults"
`

// manifestViaGenericDistrosYAML writes a genericDistroYAML for the
// given bootc container and let "images" do the work based on this
// YAML. This is a bit roundabout, we could also just feed the data
// directly into images. But lets do it like this for now because
// we expect actual YAML files as part of the bootc containers for
// advanced tweaking so dealing with files everywhere *might* be
// nice (but this is not set in stone, really an exploration)
func manifestViaGenericDistrosYAML(c *ManifestConfig, rng *rand.Rand) ([]byte, error) {
	bootcDefsDir, err := os.MkdirTemp("", "bootc-defs")
	if err != nil {
		return nil, err
	}
	//defer os.RemoveAll(bootcDefsDir)
	println("using ", bootcDefsDir)

	imgTypesPath := filepath.Join(bootcDefsDir, "defs", "distro.yaml")
	if err := os.MkdirAll(filepath.Dir(imgTypesPath), 0700); err != nil {
		return nil, err
	}

	// create a "generic" distro based on the inputs
	distroName := fmt.Sprintf("bootc-%s-%s", c.SourceInfo.OSRelease.ID, c.SourceInfo.OSRelease.VersionID)
	bootcDistroPath := filepath.Join(bootcDefsDir, "distros.yaml")
	bootcDistros := defs.DistrosYAML{
		Distros: []defs.DistroYAML{
			{
				// XXX: check what else to export here
				Name:      distroName,
				OsVersion: c.SourceInfo.OSRelease.VersionID,
				// XXX: hack
				DefaultFSType: disk.FS_EXT4,

				// XXX: what about distro_like here?

				// use relative path here
				DefsPath: "defs",
				Runner:   runner.RunnerConf{Name: "org.osbuild.linux"},
			},
		},
	}
	if err := writeAsYAML(bootcDistroPath, bootcDistros); err != nil {
		return nil, err
	}
	// XXX: create a fake repo registry, this is needed for compat
	// with the "old" way of doing things in "images". Ideally we
	// would have a way to do this with pure YAML but for now we
	// need this
	bootcReposPath := filepath.Join(bootcDefsDir, distroName+".json")
	if err := os.WriteFile(bootcReposPath, []byte(`{"x86_64":[{"name": "fake"}]}`), 0644); err != nil {
		return nil, err
	}

	// XXX: hack, put into YAML into images
	if err := os.WriteFile(imgTypesPath, []byte(bootcImageTypesContent), 0644); err != nil {
		return nil, err
	}
	// XXX: this triggers a warning currently, make this nicer
	// XXX2: this overrides any existing experimental settings :(
	os.Setenv("IMAGE_BUILDER_EXPERIMENTAL", "yamldir="+bootcDefsDir)
	fac := distrofactory.NewDefault()
	// XXX: slightly sad that we need this
	repos, err := reporegistry.New([]string{bootcDefsDir}, nil)
	if err != nil {
		return nil, err
	}
	// XXX: when this goes into ibcli we can use "getOneImage()" here isntead
	fmt.Println("requested image types:", c.ImageTypes)
	imgTypeStr := "qcow2"
	filter, err := imagefilter.New(fac, repos)
	if err != nil {
		return nil, err
	}
	res, err := filter.Filter([]string{
		"distro:" + distroName,
		"type:" + imgTypeStr,
	}...)
	if err != nil {
		return nil, err
	}
	if len(res) != 1 {
		return nil, fmt.Errorf("internal error: unexpected results for %q: %v", distroName, res)
	}
	img := res[0]
	fmt.Printf("found image type: %+v\n", img)
	// XXX: ibcli would just call generateManifest() here
	var osbuildManifestBuf bytes.Buffer
	mg, err := manifestgen.New(repos, &manifestgen.Options{Output: &osbuildManifestBuf})
	if err != nil {
		return nil, err
	}

	imgOpts := &distro.ImageOptions{
		//Facts:        &facts.ImageOptions{APIType: facts.IBCLI_APITYPE},
		Bootc: &distro.BootcRef{
			Imgref:      &c.Imgref,
			BuildImgref: &c.BuildImgref,
		},
	}

	bp := blueprint.Blueprint(*c.Config)
	if err := mg.Generate(&bp, img.Distro, img.ImgType, img.Arch, imgOpts); err != nil {
		return nil, err
	}

	return osbuildManifestBuf.Bytes(), nil
}

var (
	// The mountpoint policy for bootc images is more restrictive than the
	// ostree mountpoint policy defined in osbuild/images. It only allows /
	// (for sizing the root partition) and custom mountpoints under /var but
	// not /var itself.

	// Since our policy library doesn't support denying a path while allowing
	// its subpaths (only the opposite), we augment the standard policy check
	// with a simple search through the custom mountpoints to deny /var
	// specifically.
	mountpointPolicy = pathpolicy.NewPathPolicies(map[string]pathpolicy.PathPolicy{
		// allow all existing mountpoints (but no subdirs) to support size customizations
		"/":     {Deny: false, Exact: true},
		"/boot": {Deny: false, Exact: true},

		// /var is not allowed, but we need to allow any subdirectories that
		// are not denied below, so we allow it initially and then check it
		// separately (in checkMountpoints())
		"/var": {Deny: false},

		// /var subdir denials
		"/var/home":     {Deny: true},
		"/var/lock":     {Deny: true}, // symlink to ../run/lock which is on tmpfs
		"/var/mail":     {Deny: true}, // symlink to spool/mail
		"/var/mnt":      {Deny: true},
		"/var/roothome": {Deny: true},
		"/var/run":      {Deny: true}, // symlink to ../run which is on tmpfs
		"/var/srv":      {Deny: true},
		"/var/usrlocal": {Deny: true},
	})

	mountpointMinimalPolicy = pathpolicy.NewPathPolicies(map[string]pathpolicy.PathPolicy{
		// allow all existing mountpoints to support size customizations
		"/":     {Deny: false, Exact: true},
		"/boot": {Deny: false, Exact: true},
	})
)

func checkMountpoints(filesystems []blueprint.FilesystemCustomization, policy *pathpolicy.PathPolicies) error {
	errs := []error{}
	for _, fs := range filesystems {
		if err := policy.Check(fs.Mountpoint); err != nil {
			errs = append(errs, err)
		}
		if fs.Mountpoint == "/var" {
			// this error message is consistent with the errors returned by policy.Check()
			// TODO: remove trailing space inside the quoted path when the function is fixed in osbuild/images.
			errs = append(errs, fmt.Errorf(`path "/var" is not allowed`))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("the following errors occurred while validating custom mountpoints:\n%w", errors.Join(errs...))
	}
	return nil
}

func checkFilesystemCustomizations(fsCustomizations []blueprint.FilesystemCustomization, ptmode disk.PartitioningMode) error {
	var policy *pathpolicy.PathPolicies
	switch ptmode {
	case disk.BtrfsPartitioningMode:
		// btrfs subvolumes are not supported at build time yet, so we only
		// allow / and /boot to be customized when building a btrfs disk (the
		// minimal policy)
		policy = mountpointMinimalPolicy
	default:
		policy = mountpointPolicy
	}
	if err := checkMountpoints(fsCustomizations, policy); err != nil {
		return err
	}
	return nil
}

// updateFilesystemSizes updates the size of the root filesystem customization
// based on the minRootSize. The new min size whichever is larger between the
// existing size and the minRootSize. If the root filesystem is not already
// configured, a new customization is added.
func updateFilesystemSizes(fsCustomizations []blueprint.FilesystemCustomization, minRootSize uint64) []blueprint.FilesystemCustomization {
	updated := make([]blueprint.FilesystemCustomization, len(fsCustomizations), len(fsCustomizations)+1)
	hasRoot := false
	for idx, fsc := range fsCustomizations {
		updated[idx] = fsc
		if updated[idx].Mountpoint == "/" {
			updated[idx].MinSize = max(updated[idx].MinSize, minRootSize)
			hasRoot = true
		}
	}

	if !hasRoot {
		// no root customization found: add it
		updated = append(updated, blueprint.FilesystemCustomization{Mountpoint: "/", MinSize: minRootSize})
	}
	return updated
}

// setFSTypes sets the filesystem types for all mountable entities to match the
// selected rootfs type.
// If rootfs is 'btrfs', the function will keep '/boot' to its default.
func setFSTypes(pt *disk.PartitionTable, rootfs string) error {
	if rootfs == "" {
		return fmt.Errorf("root filesystem type is empty")
	}

	return pt.ForEachMountable(func(mnt disk.Mountable, _ []disk.Entity) error {
		switch mnt.GetMountpoint() {
		case "/boot/efi":
			// never change the efi partition's type
			return nil
		case "/boot":
			// change only if we're not doing btrfs
			if rootfs == "btrfs" {
				return nil
			}
			fallthrough
		default:
			switch elem := mnt.(type) {
			case *disk.Filesystem:
				elem.Type = rootfs
			case *disk.BtrfsSubvolume:
				// nothing to do
			default:
				return fmt.Errorf("the mountable disk entity for %q of the base partition table is not an ordinary filesystem but %T", mnt.GetMountpoint(), mnt)
			}
			return nil
		}
	})
}

func genPartitionTable(c *ManifestConfig, customizations *blueprint.Customizations, rng *rand.Rand) (*disk.PartitionTable, error) {
	fsCust := customizations.GetFilesystems()
	diskCust, err := customizations.GetPartitioning()
	if err != nil {
		return nil, fmt.Errorf("error reading disk customizations: %w", err)
	}

	// Embedded disk customization applies if there was no local customization
	if fsCust == nil && diskCust == nil && c.SourceInfo != nil && c.SourceInfo.ImageCustomization != nil {
		imageCustomizations := c.SourceInfo.ImageCustomization

		fsCust = imageCustomizations.GetFilesystems()
		diskCust, err = imageCustomizations.GetPartitioning()
		if err != nil {
			return nil, fmt.Errorf("error reading disk customizations: %w", err)
		}
	}

	var partitionTable *disk.PartitionTable
	switch {
	// XXX: move into images library
	case fsCust != nil && diskCust != nil:
		return nil, fmt.Errorf("cannot combine disk and filesystem customizations")
	case diskCust != nil:
		partitionTable, err = genPartitionTableDiskCust(c, diskCust, rng)
		if err != nil {
			return nil, err
		}
	default:
		partitionTable, err = genPartitionTableFsCust(c, fsCust, rng)
		if err != nil {
			return nil, err
		}
	}

	// Ensure ext4 rootfs has fs-verity enabled
	rootfs := partitionTable.FindMountable("/")
	if rootfs != nil {
		switch elem := rootfs.(type) {
		case *disk.Filesystem:
			if elem.Type == "ext4" {
				elem.MkfsOptions = append(elem.MkfsOptions, []disk.MkfsOption{disk.MkfsVerity}...)
			}
		}
	}

	return partitionTable, nil
}

// calcRequiredDirectorySizes will calculate the minimum sizes for /
// for disk customizations. We need this because with advanced partitioning
// we never grow the rootfs to the size of the disk (unlike the tranditional
// filesystem customizations).
//
// So we need to go over the customizations and ensure the min-size for "/"
// is at least rootfsMinSize.
//
// Note that a custom "/usr" is not supported in image mode so splitting
// rootfsMinSize between / and /usr is not a concern.
func calcRequiredDirectorySizes(distCust *blueprint.DiskCustomization, rootfsMinSize uint64) (map[string]uint64, error) {
	// XXX: this has *way* too much low-level knowledge about the
	// inner workings of blueprint.DiskCustomizations plus when
	// a new type it needs to get added here too, think about
	// moving into "images" instead (at least partly)
	mounts := map[string]uint64{}
	for _, part := range distCust.Partitions {
		switch part.Type {
		case "", "plain":
			mounts[part.Mountpoint] = part.MinSize
		case "lvm":
			for _, lv := range part.LogicalVolumes {
				mounts[lv.Mountpoint] = part.MinSize
			}
		case "btrfs":
			for _, subvol := range part.Subvolumes {
				mounts[subvol.Mountpoint] = part.MinSize
			}
		default:
			return nil, fmt.Errorf("unknown disk customization type %q", part.Type)
		}
	}
	// ensure rootfsMinSize is respected
	return map[string]uint64{
		"/": max(rootfsMinSize, mounts["/"]),
	}, nil
}

func genPartitionTableDiskCust(c *ManifestConfig, diskCust *blueprint.DiskCustomization, rng *rand.Rand) (*disk.PartitionTable, error) {
	if err := diskCust.ValidateLayoutConstraints(); err != nil {
		return nil, fmt.Errorf("cannot use disk customization: %w", err)
	}

	diskCust.MinSize = max(diskCust.MinSize, c.RootfsMinsize)

	basept, ok := partitionTables[c.Architecture.String()]
	if !ok {
		return nil, fmt.Errorf("pipelines: no partition tables defined for %s", c.Architecture)
	}
	defaultFSType, err := disk.NewFSType(c.RootFSType)
	if err != nil {
		return nil, err
	}
	requiredMinSizes, err := calcRequiredDirectorySizes(diskCust, c.RootfsMinsize)
	if err != nil {
		return nil, err
	}
	partOptions := &disk.CustomPartitionTableOptions{
		PartitionTableType: basept.Type,
		// XXX: not setting/defaults will fail to boot with btrfs/lvm
		BootMode:         platform.BOOT_HYBRID,
		DefaultFSType:    defaultFSType,
		RequiredMinSizes: requiredMinSizes,
		Architecture:     c.Architecture,
	}
	return disk.NewCustomPartitionTable(diskCust, partOptions, rng)
}

func genPartitionTableFsCust(c *ManifestConfig, fsCust []blueprint.FilesystemCustomization, rng *rand.Rand) (*disk.PartitionTable, error) {
	basept, ok := partitionTables[c.Architecture.String()]
	if !ok {
		return nil, fmt.Errorf("pipelines: no partition tables defined for %s", c.Architecture)
	}

	partitioningMode := disk.RawPartitioningMode
	if c.RootFSType == "btrfs" {
		partitioningMode = disk.BtrfsPartitioningMode
	}
	if err := checkFilesystemCustomizations(fsCust, partitioningMode); err != nil {
		return nil, err
	}
	fsCustomizations := updateFilesystemSizes(fsCust, c.RootfsMinsize)

	pt, err := disk.NewPartitionTable(&basept, fsCustomizations, DEFAULT_SIZE, partitioningMode, c.Architecture, nil, rng)
	if err != nil {
		return nil, err
	}

	if err := setFSTypes(pt, c.RootFSType); err != nil {
		return nil, fmt.Errorf("error setting root filesystem type: %w", err)
	}
	return pt, nil
}

func manifestForDiskImage(c *ManifestConfig, rng *rand.Rand) (*manifest.Manifest, error) {
	if c.Imgref == "" {
		return nil, fmt.Errorf("pipeline: no base image defined")
	}
	containerSource := container.SourceSpec{
		Source: c.Imgref,
		Name:   c.Imgref,
		Local:  true,
	}
	buildContainerSource := container.SourceSpec{
		Source: c.BuildImgref,
		Name:   c.BuildImgref,
		Local:  true,
	}

	var customizations *blueprint.Customizations
	if c.Config != nil {
		customizations = c.Config.Customizations
	}

	img := image.NewBootcDiskImage(containerSource, buildContainerSource)
	img.OSCustomizations.Users = users.UsersFromBP(customizations.GetUsers())
	img.OSCustomizations.Groups = users.GroupsFromBP(customizations.GetGroups())
	img.OSCustomizations.SELinux = c.SourceInfo.SELinuxPolicy
	img.OSCustomizations.BuildSELinux = img.OSCustomizations.SELinux
	if c.BuildSourceInfo != nil {
		img.OSCustomizations.BuildSELinux = c.BuildSourceInfo.SELinuxPolicy
	}

	img.OSCustomizations.KernelOptionsAppend = []string{
		"rw",
		// TODO: Drop this as we expect kargs to come from the container image,
		// xref https://github.com/CentOS/centos-bootc-layered/blob/main/cloud/usr/lib/bootc/install/05-cloud-kargs.toml
		"console=tty0",
		"console=ttyS0",
	}

	switch c.Architecture {
	case arch.ARCH_X86_64:
		img.Platform = &platform.X86{
			BasePlatform: platform.BasePlatform{},
			BIOS:         true,
		}
	case arch.ARCH_AARCH64:
		img.Platform = &platform.Aarch64{
			UEFIVendor: "fedora",
			BasePlatform: platform.BasePlatform{
				QCOW2Compat: "1.1",
			},
		}
	case arch.ARCH_S390X:
		img.Platform = &platform.S390X{
			BasePlatform: platform.BasePlatform{
				QCOW2Compat: "1.1",
			},
			Zipl: true,
		}
	case arch.ARCH_PPC64LE:
		img.Platform = &platform.PPC64LE{
			BasePlatform: platform.BasePlatform{
				QCOW2Compat: "1.1",
			},
			BIOS: true,
		}
	}

	if kopts := customizations.GetKernel(); kopts != nil && kopts.Append != "" {
		img.OSCustomizations.KernelOptionsAppend = append(img.OSCustomizations.KernelOptionsAppend, kopts.Append)
	}

	pt, err := genPartitionTable(c, customizations, rng)
	if err != nil {
		return nil, err
	}
	img.PartitionTable = pt

	// Check Directory/File Customizations are valid
	dc := customizations.GetDirectories()
	fc := customizations.GetFiles()
	if err := blueprint.ValidateDirFileCustomizations(dc, fc); err != nil {
		return nil, err
	}
	if err := blueprint.CheckDirectoryCustomizationsPolicy(dc, policies.OstreeCustomDirectoriesPolicies); err != nil {
		return nil, err
	}
	if err := blueprint.CheckFileCustomizationsPolicy(fc, policies.OstreeCustomFilesPolicies); err != nil {
		return nil, err
	}
	img.OSCustomizations.Files, err = blueprint.FileCustomizationsToFsNodeFiles(fc)
	if err != nil {
		return nil, err
	}
	img.OSCustomizations.Directories, err = blueprint.DirectoryCustomizationsToFsNodeDirectories(dc)
	if err != nil {
		return nil, err
	}

	// For the bootc-disk image, the filename is the basename and the extension
	// is added automatically for each disk format
	img.Filename = "disk"

	mf := manifest.New()
	mf.Distro = manifest.DISTRO_FEDORA
	runner := &runner.Linux{}

	if err := img.InstantiateManifestFromContainers(&mf, []container.SourceSpec{containerSource}, runner, rng); err != nil {
		return nil, err
	}

	return &mf, nil
}

func labelForISO(os *source.OSRelease, arch *arch.Arch) string {
	switch os.ID {
	case "fedora":
		return fmt.Sprintf("Fedora-S-dvd-%s-%s", arch, os.VersionID)
	case "centos":
		labelTemplate := "CentOS-Stream-%s-BaseOS-%s"
		if os.VersionID == "8" {
			labelTemplate = "CentOS-Stream-%s-%s-dvd"
		}
		return fmt.Sprintf(labelTemplate, os.VersionID, arch)
	case "rhel":
		version := strings.ReplaceAll(os.VersionID, ".", "-")
		return fmt.Sprintf("RHEL-%s-BaseOS-%s", version, arch)
	default:
		return fmt.Sprintf("Container-Installer-%s", arch)
	}
}

func needsRHELLoraxTemplates(si source.OSRelease) bool {
	return si.ID == "rhel" || slices.Contains(si.IDLike, "rhel") || si.VersionID == "eln"
}

func manifestForISO(c *ManifestConfig, rng *rand.Rand) (*manifest.Manifest, error) {
	if c.Imgref == "" {
		return nil, fmt.Errorf("pipeline: no base image defined")
	}

	imageDef, err := distrodef.LoadImageDef(c.DistroDefPaths, c.SourceInfo.OSRelease.ID, c.SourceInfo.OSRelease.VersionID, "anaconda-iso")
	if err != nil {
		return nil, err
	}

	containerSource := container.SourceSpec{
		Source: c.Imgref,
		Name:   c.Imgref,
		Local:  true,
	}

	// The ref is not needed and will be removed from the ctor later
	// in time
	img := image.NewAnacondaContainerInstaller(containerSource, "")
	img.ContainerRemoveSignatures = true
	img.RootfsCompression = "zstd"

	img.Product = c.SourceInfo.OSRelease.Name
	img.OSVersion = c.SourceInfo.OSRelease.VersionID

	img.ExtraBasePackages = rpmmd.PackageSet{
		Include: imageDef.Packages,
	}

	img.ISOLabel = labelForISO(&c.SourceInfo.OSRelease, &c.Architecture)

	var customizations *blueprint.Customizations
	if c.Config != nil {
		customizations = c.Config.Customizations
	}
	img.FIPS = customizations.GetFIPS()
	img.Kickstart, err = kickstart.New(customizations)
	if err != nil {
		return nil, err
	}
	img.Kickstart.Path = osbuild.KickstartPathOSBuild
	if kopts := customizations.GetKernel(); kopts != nil && kopts.Append != "" {
		img.Kickstart.KernelOptionsAppend = append(img.Kickstart.KernelOptionsAppend, kopts.Append)
	}
	img.Kickstart.NetworkOnBoot = true

	instCust, err := customizations.GetInstaller()
	if err != nil {
		return nil, err
	}
	if instCust != nil && instCust.Modules != nil {
		img.AdditionalAnacondaModules = append(img.AdditionalAnacondaModules, instCust.Modules.Enable...)
		img.DisabledAnacondaModules = append(img.DisabledAnacondaModules, instCust.Modules.Disable...)
	}
	img.AdditionalAnacondaModules = append(img.AdditionalAnacondaModules,
		anaconda.ModuleUsers,
		anaconda.ModuleServices,
		anaconda.ModuleSecurity,
	)

	img.Kickstart.OSTree = &kickstart.OSTree{
		OSName: "default",
	}
	img.UseRHELLoraxTemplates = needsRHELLoraxTemplates(c.SourceInfo.OSRelease)

	switch c.Architecture {
	case arch.ARCH_X86_64:
		img.Platform = &platform.X86{
			BasePlatform: platform.BasePlatform{
				ImageFormat: platform.FORMAT_ISO,
			},
			BIOS:       true,
			UEFIVendor: c.SourceInfo.UEFIVendor,
		}
		img.ISOBoot = manifest.Grub2ISOBoot
	case arch.ARCH_AARCH64:
		// aarch64 always uses UEFI, so let's enforce the vendor
		if c.SourceInfo.UEFIVendor == "" {
			return nil, fmt.Errorf("UEFI vendor must be set for aarch64 ISO")
		}
		img.Platform = &platform.Aarch64{
			BasePlatform: platform.BasePlatform{
				ImageFormat: platform.FORMAT_ISO,
			},
			UEFIVendor: c.SourceInfo.UEFIVendor,
		}
	case arch.ARCH_S390X:
		img.Platform = &platform.S390X{
			Zipl: true,
			BasePlatform: platform.BasePlatform{
				ImageFormat: platform.FORMAT_ISO,
			},
		}
	case arch.ARCH_PPC64LE:
		img.Platform = &platform.PPC64LE{
			BIOS: true,
			BasePlatform: platform.BasePlatform{
				ImageFormat: platform.FORMAT_ISO,
			},
		}
	default:
		return nil, fmt.Errorf("unsupported architecture %v", c.Architecture)
	}
	// see https://github.com/osbuild/bootc-image-builder/issues/733
	img.RootfsType = manifest.SquashfsRootfs
	img.Filename = "install.iso"

	mf := manifest.New()

	foundDistro, foundRunner, err := getDistroAndRunner(c.SourceInfo.OSRelease)
	if err != nil {
		return nil, fmt.Errorf("failed to infer distro and runner: %w", err)
	}
	mf.Distro = foundDistro

	_, err = img.InstantiateManifest(&mf, nil, foundRunner, rng)
	return &mf, err
}

func getDistroAndRunner(osRelease source.OSRelease) (manifest.Distro, runner.Runner, error) {
	switch osRelease.ID {
	case "fedora":
		version, err := strconv.ParseUint(osRelease.VersionID, 10, 64)
		if err != nil {
			return manifest.DISTRO_NULL, nil, fmt.Errorf("cannot parse Fedora version (%s): %w", osRelease.VersionID, err)
		}

		return manifest.DISTRO_FEDORA, &runner.Fedora{
			Version: version,
		}, nil
	case "centos":
		version, err := strconv.ParseUint(osRelease.VersionID, 10, 64)
		if err != nil {
			return manifest.DISTRO_NULL, nil, fmt.Errorf("cannot parse CentOS version (%s): %w", osRelease.VersionID, err)
		}
		r := &runner.CentOS{
			Version: version,
		}
		switch version {
		case 9:
			return manifest.DISTRO_EL9, r, nil
		case 10:
			return manifest.DISTRO_EL10, r, nil
		default:
			logrus.Warnf("Unknown CentOS version %d, using default distro for manifest generation", version)
			return manifest.DISTRO_NULL, r, nil
		}

	case "rhel":
		versionParts := strings.Split(osRelease.VersionID, ".")
		if len(versionParts) != 2 {
			return manifest.DISTRO_NULL, nil, fmt.Errorf("invalid RHEL version format: %s", osRelease.VersionID)
		}
		major, err := strconv.ParseUint(versionParts[0], 10, 64)
		if err != nil {
			return manifest.DISTRO_NULL, nil, fmt.Errorf("cannot parse RHEL major version (%s): %w", versionParts[0], err)
		}
		minor, err := strconv.ParseUint(versionParts[1], 10, 64)
		if err != nil {
			return manifest.DISTRO_NULL, nil, fmt.Errorf("cannot parse RHEL minor version (%s): %w", versionParts[1], err)
		}
		r := &runner.RHEL{
			Major: major,
			Minor: minor,
		}
		switch major {
		case 9:
			return manifest.DISTRO_EL9, r, nil
		case 10:
			return manifest.DISTRO_EL10, r, nil
		default:
			logrus.Warnf("Unknown RHEL version %d, using default distro for manifest generation", major)
			return manifest.DISTRO_NULL, r, nil
		}
	}

	logrus.Warnf("Unknown distro %s, using default runner", osRelease.ID)
	return manifest.DISTRO_NULL, &runner.Linux{}, nil
}

func createRand() *rand.Rand {
	seed, err := cryptorand.Int(cryptorand.Reader, big.NewInt(math.MaxInt64))
	if err != nil {
		panic("Cannot generate an RNG seed.")
	}

	// math/rand is good enough in this case
	/* #nosec G404 */
	return rand.New(rand.NewSource(seed.Int64()))
}
