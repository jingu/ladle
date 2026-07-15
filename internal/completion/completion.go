// Package completion provides shell completion script generation.
package completion

import (
	"fmt"
	"io"
)

// Shell represents a supported shell type.
type Shell string

const (
	ShellBash Shell = "bash"
	ShellZsh  Shell = "zsh"
	ShellFish Shell = "fish"
)

// Generate writes a shell completion script to the given writer.
func Generate(w io.Writer, shell Shell) error {
	switch shell {
	case ShellBash:
		return generateBash(w)
	case ShellZsh:
		return generateZsh(w)
	case ShellFish:
		return generateFish(w)
	default:
		return fmt.Errorf("unsupported shell: %s (supported: bash, zsh, fish)", shell)
	}
}

func generateBash(w io.Writer) error {
	script := `# ladle bash completion
_ladle_completions() {
    local cur prev opts cmd
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    cmd="${COMP_WORDS[0]}"
    opts="--meta --versions --editor --profile --region --account --project --endpoint-url --no-sign-request --yes --force --dry-run --help --version"
    commands="cp skill s3:// gs:// az:// r2:// ssm://"

    if [[ "${COMP_CWORD}" -eq 1 && "$cur" != *://* && "$cur" != -* ]]; then
        COMPREPLY=( $(compgen -W "${commands}" -- "${cur}") )
        return 0
    fi

    # Extract --profile, --account and --project values from command line
    local profile_arg="" account_arg="" project_arg=""
    for ((i=0; i<${#COMP_WORDS[@]}; i++)); do
        if [[ "${COMP_WORDS[$i]}" == "--profile" ]]; then
            profile_arg="--profile ${COMP_WORDS[$((i+1))]}"
        elif [[ "${COMP_WORDS[$i]}" == --profile=* ]]; then
            profile_arg="--profile ${COMP_WORDS[$i]#--profile=}"
        elif [[ "${COMP_WORDS[$i]}" == "--account" ]]; then
            account_arg="--account ${COMP_WORDS[$((i+1))]}"
        elif [[ "${COMP_WORDS[$i]}" == --account=* ]]; then
            account_arg="--account ${COMP_WORDS[$i]#--account=}"
        elif [[ "${COMP_WORDS[$i]}" == "--project" ]]; then
            project_arg="--project ${COMP_WORDS[$((i+1))]}"
        elif [[ "${COMP_WORDS[$i]}" == --project=* ]]; then
            project_arg="--project ${COMP_WORDS[$i]#--project=}"
        fi
    done

    # Complete cloud storage URIs (s3://, gs://, or az://)
    if [[ "$cur" == s3://* || "$cur" == gs://* || "$cur" == az://* ]]; then
        local scheme="${cur%%://*}"
        local bucket_and_path="${cur#*://}"
        local bucket="${bucket_and_path%%/*}"

        if [[ "$bucket_and_path" == */* ]]; then
            # Complete keys within bucket
            local prefix="${bucket_and_path#*/}"
            local result
            result=$("$cmd" --complete-path $profile_arg $account_arg $project_arg "${scheme}://${bucket}/${prefix}" 2>/dev/null)
            if [[ -n "$result" ]]; then
                while IFS= read -r line; do
                    COMPREPLY+=("$line")
                done <<< "$result"
            fi
        else
            # Complete bucket names
            local result
            result=$("$cmd" --complete-bucket $profile_arg $account_arg $project_arg "${scheme}://$bucket" 2>/dev/null)
            if [[ -n "$result" ]]; then
                while IFS= read -r b; do
                    COMPREPLY+=("${scheme}://${b}/")
                done <<< "$result"
            fi
        fi
        return 0
    fi

    case "${prev}" in
        --profile|--region|--account|--project|--endpoint-url|--editor)
            return 0
            ;;
    esac

    if [[ "$cur" == -* ]]; then
        COMPREPLY=( $(compgen -W "${opts}" -- "${cur}") )
        return 0
    fi
}
complete -o nospace -F _ladle_completions ladle
complete -o nospace -F _ladle_completions ./ladle
`
	_, err := fmt.Fprint(w, script)
	return err
}

func generateZsh(w io.Writer) error {
	script := `#compdef ladle

_ladle() {
    local -a opts
    opts=(
        '--meta[Edit metadata instead of file content]'
        '--versions[Show version history for a file]'
        '--editor[Specify editor command]:editor:'
        '--profile[AWS named profile]:profile:'
        '--region[AWS region]:region:'
        '--account[Azure storage account name]:account:'
        '--project[GCP project ID for bucket listing]:project:'
        '--endpoint-url[Custom endpoint URL]:url:'
        '--no-sign-request[Do not sign requests]'
        '--yes[Skip confirmation prompt]'
        '--force[Force operation on binary files]'
        '--dry-run[Show diff without uploading]'
        '--help[Show help]'
        '--version[Show version]'
    )

    if (( CURRENT == 2 )); then
        if [[ "${words[CURRENT]}" == *://* ]]; then
            _ladle_uri
        else
            local -a commands
            commands=(
                'cp:Copy an object with its metadata'
                'skill:Manage the ladle Agent Skill'
                's3://:Amazon S3 URI'
                'gs://:Google Cloud Storage URI'
                'az://:Azure Blob Storage URI'
                'r2://:Cloudflare R2 URI'
                'ssm://:AWS Systems Manager Parameter URI'
            )
            _describe -t commands 'ladle command' commands
        fi
        return
    fi

    _arguments -s $opts '*:uri:_ladle_uri'
}

_ladle_uri() {
    local cur="${words[CURRENT]}"
    local cmd="${words[1]}"

    # Extract --profile, --account and --project values from command line
    local -a profile_arg account_arg project_arg
    local -i i
    for ((i=1; i<$#words; i++)); do
        if [[ "${words[$i]}" == "--profile" ]]; then
            profile_arg=(--profile "${words[$((i+1))]}")
        elif [[ "${words[$i]}" == --profile=* ]]; then
            profile_arg=(--profile "${words[$i]#--profile=}")
        elif [[ "${words[$i]}" == "--account" ]]; then
            account_arg=(--account "${words[$((i+1))]}")
        elif [[ "${words[$i]}" == --account=* ]]; then
            account_arg=(--account "${words[$i]#--account=}")
        elif [[ "${words[$i]}" == "--project" ]]; then
            project_arg=(--project "${words[$((i+1))]}")
        elif [[ "${words[$i]}" == --project=* ]]; then
            project_arg=(--project "${words[$i]#--project=}")
        fi
    done

    if [[ "$cur" == s3://* || "$cur" == gs://* || "$cur" == az://* ]]; then
        local scheme="${cur%%://*}"
        local bucket_and_path="${cur#*://}"
        local bucket="${bucket_and_path%%/*}"

        if [[ "$bucket_and_path" == */* ]]; then
            local prefix="${bucket_and_path#*/}"
            local -a completions
            completions=(${(f)"$("$cmd" --complete-path $profile_arg $account_arg $project_arg "${scheme}://${bucket}/${prefix}" 2>/dev/null)"})
            compadd -S '' -q -- $completions
        else
            local -a buckets
            buckets=(${(f)"$("$cmd" --complete-bucket $profile_arg $account_arg $project_arg "${scheme}://$bucket" 2>/dev/null)"})
            compadd -S '/' -q -- ${buckets[@]/#/${scheme}://}
        fi
    else
        compadd -S '://' -- s3 gs az r2 ssm
    fi
}

compdef _ladle ladle
compdef _ladle ./ladle
`
	_, err := fmt.Fprint(w, script)
	return err
}

func generateFish(w io.Writer) error {
	script := `# ladle fish completion
complete -c ladle -l meta -d 'Edit metadata instead of file content'
complete -c ladle -l versions -d 'Show version history for a file'
complete -c ladle -l editor -r -d 'Specify editor command'
complete -c ladle -l profile -r -d 'AWS named profile'
complete -c ladle -l region -r -d 'AWS region'
complete -c ladle -l account -r -d 'Azure storage account name'
complete -c ladle -l project -r -d 'GCP project ID for bucket listing'
complete -c ladle -l endpoint-url -r -d 'Custom endpoint URL'
complete -c ladle -l no-sign-request -d 'Do not sign requests'
complete -c ladle -s y -l yes -d 'Skip confirmation prompt'
complete -c ladle -l force -d 'Force operation on binary files'
complete -c ladle -l dry-run -d 'Show diff without uploading'
complete -c ladle -l help -d 'Show help'
complete -c ladle -l version -d 'Show version'

complete -c ladle -n '__fish_use_subcommand' -a cp -d 'Copy an object with its metadata'
function __ladle_complete_uri
    set -l cur (commandline -ct)
    set -l cmd (commandline -po)[1]

    # Extract --profile, --account and --project values from command line
    set -l profile_arg
    set -l account_arg
    set -l project_arg
    set -l tokens (commandline -po)
    for i in (seq (count $tokens))
        if test "$tokens[$i]" = "--profile"
            set profile_arg --profile $tokens[(math $i + 1)]
        else if string match -q '--profile=*' -- "$tokens[$i]"
            set profile_arg --profile (string replace '--profile=' '' -- "$tokens[$i]")
        else if test "$tokens[$i]" = "--account"
            set account_arg --account $tokens[(math $i + 1)]
        else if string match -q '--account=*' -- "$tokens[$i]"
            set account_arg --account (string replace '--account=' '' -- "$tokens[$i]")
        else if test "$tokens[$i]" = "--project"
            set project_arg --project $tokens[(math $i + 1)]
        else if string match -q '--project=*' -- "$tokens[$i]"
            set project_arg --project (string replace '--project=' '' -- "$tokens[$i]")
        end
    end

    if string match -q 's3://*' -- $cur; or string match -q 'gs://*' -- $cur; or string match -q 'az://*' -- $cur
        set -l scheme (string split '://' -- $cur)[1]
        set -l bucket_and_path (string replace "$scheme://" '' -- $cur)
        if string match -q '*/*' -- $bucket_and_path
            $cmd --complete-path $profile_arg $account_arg $project_arg $cur 2>/dev/null
        else
            for b in ($cmd --complete-bucket $profile_arg $account_arg $project_arg "$scheme://$bucket_and_path" 2>/dev/null)
                echo "$scheme://$b/"
            end
        end
    else
        printf '%s\n' 's3://' 'gs://' 'az://' 'r2://' 'ssm://'
    end
end

complete -c ladle -a '(__ladle_complete_uri)' -f
complete -c ./ladle -a '(__ladle_complete_uri)' -f
`
	_, err := fmt.Fprint(w, script)
	return err
}
