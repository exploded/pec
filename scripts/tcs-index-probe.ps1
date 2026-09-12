<#
tcs-index-probe.ps1 - read-only probe for milestone 4.

Does the Bisque TCS window expose its live PEC index to Windows UI
Automation? If it does, pec can read the index without screen capture.

Run with TheSkyX open, the mount connected and TRACKING ON (the index only
advances while tracking; the roof can stay closed). The script never sends
anything to TheSkyX or the mount; it only reads what Windows already shows.

    powershell -ExecutionPolicy Bypass -File scripts\tcs-index-probe.ps1

Paste the whole output back. Options:
    -Title 'TCS'      substring of the window title to look for
    -Samples 6        how many times to read every text element
    -IntervalS 1.5    seconds between reads
    -Dump             also list every text element found (long)
#>
param(
    [string]$Title = 'TCS',
    [int]$Samples = 6,
    [double]$IntervalS = 1.5,
    [switch]$Dump
)

Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
$A = [System.Windows.Automation.AutomationElement]
$root = $A::RootElement
$true_ = [System.Windows.Automation.Condition]::TrueCondition
$desc = [System.Windows.Automation.TreeScope]::Descendants
$children = [System.Windows.Automation.TreeScope]::Children

function Text-Of($el) {
    $v = $null
    try {
        $p = $el.GetCurrentPattern([System.Windows.Automation.ValuePattern]::Pattern)
        if ($p) { $v = $p.Current.Value }
    } catch {}
    if (-not $v) { $v = $el.Current.Name }
    return "$v"
}

"pec TCS index probe  $(Get-Date -Format s)"
"Windows containing '$Title' in the title:"
$wins = @()
foreach ($w in $root.FindAll($children, $true_)) {
    $n = $w.Current.Name
    if ($n -like "*$Title*") { $wins += $w; "  [$($w.Current.ProcessId)] $n  ($($w.Current.ClassName))" }
}
if ($wins.Count -eq 0) {
    "  none. Top-level windows with 'Sky' or 'Bisque' in the name:"
    foreach ($w in $root.FindAll($children, $true_)) {
        $n = $w.Current.Name
        if ($n -match 'Sky|Bisque') { $wins += $w; "  [$($w.Current.ProcessId)] $n  ($($w.Current.ClassName))" }
    }
}
if ($wins.Count -eq 0) { "No candidate window found. Is TheSkyX open and the TCS window visible?"; exit 1 }

# Collect every element that shows an integer (the PEC index 0-1249, but
# also the Show Status cells such as "Current Position" -59 and
# "Current Encoder" 47,776, which the index is probably derived from), and
# sample them repeatedly.
$track = @{}
$names = @{}
for ($s = 0; $s -lt $Samples; $s++) {
    $stamp = (Get-Date).ToString('HH:mm:ss.fff')
    foreach ($w in $wins) {
        $els = $w.FindAll($desc, $true_)
        $i = 0
        $prev = ''
        foreach ($el in $els) {
            $i++
            $t = (Text-Of $el).Trim()
            if ($Dump -and $s -eq 0 -and $t) { "    #$i $($el.Current.ControlType.ProgrammaticName) id='$($el.Current.AutomationId)' '$t'" }
            if ($t -match '^-?\d{1,3}(,\d{3})*$' -or $t -match '^-?\d{1,9}$') {
                $key = "$($w.Current.ProcessId)/$($el.Current.ControlType.ProgrammaticName)/$($el.Current.AutomationId)/#$i"
                if (-not $track.ContainsKey($key)) { $track[$key] = New-Object System.Collections.ArrayList; $names[$key] = $prev }
                [void]$track[$key].Add(@{ t = $stamp; v = [long]($t -replace ',', '') })
            } elseif ($t) { $prev = $t }   # the label cell before a value cell
        }
    }
    if ($s -lt $Samples - 1) { Start-Sleep -Milliseconds ([int]($IntervalS * 1000)) }
}

""
"Integer-valued elements and what they showed over $Samples reads, $IntervalS s apart"
"(a changing one labelled with the PEC index should run at about 8.33/s and wrap at 1250;"
" Current Position should run at a steady rate in motor steps/s while tracking):"
$found = $false
foreach ($k in $track.Keys | Sort-Object) {
    $vals = ($track[$k] | ForEach-Object { $_.v }) -join ' '
    $first = $track[$k][0].v; $last = $track[$k][-1].v
    $changed = ($track[$k] | ForEach-Object { $_.v } | Select-Object -Unique).Count -gt 1
    $rate = ''
    if ($changed) {
        $span = ($Samples - 1) * $IntervalS
        $d = ($last - $first)
        if ($first -ge 0 -and $last -ge 0 -and $first -le 1249 -and $last -le 1249 -and $d -lt 0) { $d += 1250 }
        $rate = '  rate {0:N2}/s' -f ($d / $span)
        $found = $true
    }
    $label = $names[$k]; if ($label) { $label = " [$label]" }
    "  $k$label : $vals$rate"
}
""
if ($found) {
    "RESULT: changing numeric elements were found. Milestone 4 can poll them directly (no screen capture)."
} else {
    "RESULT: no changing numeric element. Either the values are drawn as pixels (screen capture path) or tracking is off."
    "Re-run with -Dump to list every text element the window exposes."
}
"Tip: run once on the 'Periodic Error Correction' tab and once on 'Show Status'; Qt may only expose the visible tab."
