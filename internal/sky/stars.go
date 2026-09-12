package sky

// Star is a bright named star TheSkyX's Find box accepts by name.
type Star struct {
	Name    string
	Bayer   string // spelled the way Find accepts it
	HIP     int
	RAHours float64 // J2000
	DecDeg  float64 // J2000
	Mag     float64
}

// Stars are the bright named stars within 20 degrees of the celestial
// equator, so one is always within an hour or two of any meridian. J2000
// positions; the 0.4 degree of precession since then does not matter for
// aiming a guide field.
var Stars = []Star{
	{"Algenib", "Gamma Pegasi", 1067, 0.2206, 15.18, 2.8},
	{"Diphda", "Beta Ceti", 3419, 0.7264, -17.99, 2.0},
	{"Menkar", "Alpha Ceti", 14135, 3.0380, 4.09, 2.5},
	{"Aldebaran", "Alpha Tauri", 21421, 4.5987, 16.51, 0.9},
	{"Rigel", "Beta Orionis", 24436, 5.2423, -8.20, 0.1},
	{"Bellatrix", "Gamma Orionis", 25336, 5.4189, 6.35, 1.6},
	{"Mintaka", "Delta Orionis", 25930, 5.5334, -0.30, 2.2},
	{"Alnilam", "Epsilon Orionis", 26311, 5.6036, -1.20, 1.7},
	{"Alnitak", "Zeta Orionis", 26727, 5.6793, -1.94, 1.8},
	{"Saiph", "Kappa Orionis", 27366, 5.7959, -9.67, 2.1},
	{"Betelgeuse", "Alpha Orionis", 27989, 5.9195, 7.41, 0.5},
	{"Alhena", "Gamma Geminorum", 31681, 6.6286, 16.40, 1.9},
	{"Sirius", "Alpha Canis Majoris", 32349, 6.7525, -16.72, -1.5},
	{"Procyon", "Alpha Canis Minoris", 37279, 7.6550, 5.22, 0.4},
	{"Alphard", "Alpha Hydrae", 46390, 9.4598, -8.66, 2.0},
	{"Regulus", "Alpha Leonis", 49669, 10.1395, 11.97, 1.4},
	{"Denebola", "Beta Leonis", 57632, 11.8177, 14.57, 2.1},
	{"Gienah", "Gamma Corvi", 59803, 12.2633, -17.54, 2.6},
	{"Porrima", "Gamma Virginis", 61941, 12.6944, -1.45, 2.7},
	{"Spica", "Alpha Virginis", 65474, 13.4199, -11.16, 1.0},
	{"Zubenelgenubi", "Alpha Librae", 72622, 14.8479, -16.04, 2.7},
	{"Zubeneschamali", "Beta Librae", 74785, 15.2834, -9.38, 2.6},
	{"Unukalhai", "Alpha Serpentis", 77070, 15.7378, 6.43, 2.6},
	{"Sabik", "Eta Ophiuchi", 84012, 17.1730, -15.72, 2.4},
	{"Rasalhague", "Alpha Ophiuchi", 86032, 17.5822, 12.56, 2.1},
	{"Cebalrai", "Beta Ophiuchi", 86742, 17.7245, 4.57, 2.8},
	{"Altair", "Alpha Aquilae", 97649, 19.8464, 8.87, 0.8},
	{"Sadalsuud", "Beta Aquarii", 106278, 21.5259, -5.57, 2.9},
	{"Enif", "Epsilon Pegasi", 107315, 21.7364, 9.88, 2.4},
	{"Deneb Algedi", "Delta Capricorni", 107556, 21.7840, -16.13, 2.9},
	{"Sadalmelik", "Alpha Aquarii", 109074, 22.0964, -0.32, 3.0},
	{"Skat", "Delta Aquarii", 113136, 22.9108, -15.82, 3.3},
	{"Markab", "Alpha Pegasi", 113963, 23.0794, 15.21, 2.5},
}
